# w8b-polish: qompack daemon refuses when disabled (D36), wave-7b nits, six commands

Branch `closeout/w8b-polish`. Workflow `wf_9fc9b4b8-2d9`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

> **Coordinator note.** `726e4489` is the Qompack.md content digest pinned in test/guards/qompackrevision_test.go, not a commit. The v1.7 revision and its errata record are ratified under D33 (the V4-ALL-08 revision guard requires the version line, the errata record and the re-pinned digest together).

## Implementer — status `done`, head `3e8cb08b`

### Root cause

D36(b): runDaemon (internal/cli/daemon.go) never read runtime.daemon.enabled before it took the writer lease. It loaded the config only after acquireWriterLease and paths.EnsureLayout, and even then it used the value only to build Options. So a hand start, or an older binary's spawner, got a resident daemon plus run/, daemon.lock and the socket in a project whose config disables the daemon, which breaks docs/release.md section 4. In the red run on the base, all five disabled-project cases started a daemon that ran until the test's 30 s context expired and exited 0 (runs/daemon-disabled-red.txt). Each doc and comment nit was checked against the code before it was edited (details in the summary).

### Summary

W8B-POLISH report (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w8b-polish, branch closeout/w8b-polish, base closeout/integration 399a141, HEAD 3e8cb08b; 8 commits, unpushed)

(1) D36(b): `qompack daemon` refuses a daemon-disabled project
- Failing test first. internal/cli/daemon_disabled_test.go TestDaemon_RefusesWhenDaemonDisabled has five subtests, and each disables the daemon through a different layer: the project file, the user file under a fake HOME/USERPROFILE, the environment (QOMPACK_RUNTIME__DAEMON__ENABLED=false), `--set runtime.daemon.enabled=false`, and the project file with --foreground. Each subtest asserts:
  - a non-zero exit;
  - exactly one stderr line, containing "disabled for this project", the key and where the value came from;
  - no "starting for project" line, and empty stdout;
  - no .qompack/run/ and no daemon.lock;
  - the .qompack/ listing is identical before and after the run;
  - no Unix socket file on POSIX, and nothing answering ipc.Probe.
  Red on the base: all five exited 0 after running a real daemon for 30 s (runs/daemon-disabled-red.txt).
- Fix (2cca561b). New refuseDisabledDaemon(env, root) runs right after the D18 home-root refusal and before acquireWriterLease:
  - It calls config.Load with the same Env that runDaemon's later load uses. config.Load only reads files.
  - If the merged value is false, it returns an error. Dispatch prints it as one line and exits ExitError (1): `qompack daemon: the daemon is disabled for this project: runtime.daemon.enabled is false (project layer, <path>\.qompack\config.json:1); set runtime.daemon.enabled to true to enable it`. The env and --set cases name the layer the same way: "env layer, QOMPACK_RUNTIME__DAEMON__ENABLED" and "flag layer, --set".
  - A config that cannot be loaded falls back to the defaults (daemon enabled), the same rule self-test and runDaemon's own load use.
  - The key is the named constant daemonEnabledKey. It is a config key name, not a budget.
  - runDaemon's "never surface a non-zero exit" doc comment now names this as its one deliberate exception, with the reason: no spawner in this build launches a daemon for such a project, and SpawnDetached calls Process.Release, so no spawner waits for the exit status.
- Enabled path unchanged. The control TestDaemon_RunsWhenTheMergedConfigEnablesIt has a user file that disables the daemon and a project file that re-enables it: the daemon starts, answers and exits 0. The existing TestDaemon_* tests pass untouched.
- Test and e2e audit. grep for runtime.daemon.enabled / "enabled":false and for every `daemon` command launch (test/e2e shutdown_spawn_inflight_test.go, test/fault resources_test.go, test/bench/hotpath process.go, internal/cli backup_test.go, homeroot_test.go):
  - No test starts the daemon command in a daemon-disabled project.
  - The disabled-project tests (test/e2e v5_x15 daemon_disabled, test/release TestSwitch_DaemonDisabled, internal/cli hooks/selftest/clients tests) run hooks or clients only, never `qompack daemon`.
  - Nothing needed changing.
- Docs: troubleshooting.md section 8 Step 4 now says the daemon command refuses such a project and quotes the refusal line.

(2) Wave-7b nits, each checked against the code first
- docs/cannot-do.md (4f52a720). The verifier was right: render.go prints the whole DaemonStatus.Counters map, and wire_checkpoint.go defines both checkpoint.cadence.local_seal and checkpoint.sources.unavailable. The text now reads: status shows no checkpoint list or latest seq, and checkpoint activity appears only as the checkpoint_finalize latency (the PreCompact row and budget B-E) and as any checkpoint.* counters the daemon has recorded, e.g. those two. fsck still "verifies the checkpoint tier against its manifest".
- docs/troubleshooting.md, both watcher passages (4750b952). spool_watch.go lookAtClientSpools keeps its entries map across kicks. A spool written after the trailing look is absent at that look. The next kick sees it as new (unsettled) and schedules a look one interval later, and that look finds it settled with passes==0, so the pass runs then. Both passages now say "about one interval (2 s) after the next request the daemon serves". The within-one-interval case ("about two intervals after that arrival") was already correct and is unchanged.
- packaging/README.md, long-path risk item (0104ca50). It now names the bundle's longest path, `.claude-plugin/plugin.json`, and notes that the longest command file is `commands/dropped.md`. Checked against plugin/ and TestAssembleBundle_Layout's file list.
- internal/checkpoint/draft.go SetCurrentWork comment (0104ca50). It now reads "SP-12's todo state (and any future manual checkpoint route; 0.3.0 ships none, D36)". Comment only.
- docs/install.md section 3 (887a3cb1). From `claude plugin install --help` and `claude plugin update --help` on 2.1.280. These were help output only: no session was started and the real ~/.claude was not touched.
  - It quotes the full install/update --json parenthetical: "(same exit codes; a marketplace-declared command is still shown and must be confirmed — pass -y when not interactive)".
  - It adds `--accept-command <sha256>` with a quote from its help text, and says a local-directory bundle declares no command.
  - The uninstall text "(same exit codes; not with --prune)" is kept.
  - It says 2.1.263 had neither flag, and that both are recorded from help and not rehearsed.

(3) Six commands (62cfb863, 92b6be37)
- Qompack.md: section 7.5 now reads "seven hooks, six commands (0.3.0 ships no manual checkpoint command)". A new Revision log entry, "v1.7 — 2026-09-27", cites D33 and D36: why the command went, what is unchanged, that SP14-M3-01 returns with a future route, and that ${CLAUDE_SESSION_ID} can bind it.
- plans/00-ARCHITECTURE.md:
  - section 3.4: "six files", with a dated note that it was seven until D36;
  - section 5.17: the Name() list drops checkpoint, and the Deps note says CheckpointNow was removed under D36;
  - the plugin-validate row: "6 commands (7 until D36 ...)".
  - 00-ARCHITECTURE has no section 7.5 of its own; its §7.5 references are to Qompack.md and are covered by the above.
- plans/TRACEABILITY.md section 4: the row now lists six command names, with the D36 and SP14-M3-01 note.
- plans/V5-SP-14-slash-commands-and-observability.md: the SP14-M3-01 row is marked "Retired 2026-09-27 (D36)" and keeps its old text as "Was: ...". A dated D36 note says it is retired, not satisfied (its gate TestCheckpoint_NeverRequestsNativeCompaction was deleted with cmd_checkpoint.go), that it returns with any checkpoint-now route (${CLAUDE_SESSION_ID} on 2.1.280), and that "seven" in this plan reads six for 0.3.0.

Commands and results (Windows unless noted; the machine was loaded by other seats; no wall-clock failures)
- Red: `go test ./internal/cli -run '^TestDaemon_RefusesWhenDaemonDisabled$' -count=1 -timeout=10m` exit 1, all five subtests "Should not be: 0" (runs/daemon-disabled-red.txt).
- Green: `go test ./internal/cli -run '^(TestDaemon_RefusesWhenDaemonDisabled|TestDaemon_RunsWhenTheMergedConfigEnablesIt)$' -count=1 -v -timeout=10m` exit 0 (runs/daemon-disabled-green.txt). <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- Full packages, `go test ./internal/<pkg> -count=1 -timeout=30m`:
  - internal/cli: ok, 57.6 s
  - internal/commands: ok, 3.7 s
  - internal/checkpoint: ok, 82.4 s
  - logs: runs/pkg-*-windows.txt
- `go test ./test/docs -count=1 -timeout=30m`: ok.
- `go test ./test/guards -count=1 -timeout=30m`: FAIL, only in TestCarriedDefects_WaveReportRequiresResolution (SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2, SP08-D3 still deferred:V6-VERIFY while plans/V6-report.md exists). This failure predates this branch: it is the known open item O4 in linux/report.md, and neither of the files it reads was touched here. The three Qompack revision guards pass: `go test ./test/guards -run '^(TestQompackChangesOnlyThroughAnAuthorizedRevision|TestQompackDeclaredVersionIsRecorded|TestQompackErrataAndRevisionLogAgree)$' -count=1 -v`. <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go run ./tools/devtool plugin-validate`: OK (9 files, 6 commands, 7 hook events).
- `go run ./tools/devtool gen-config-docs --check` and `gen-command-docs --check`: up to date.
- `go run ./tools/devtool fmt-check`: exit 0.
- `go vet ./internal/cli ./internal/checkpoint`, on Windows and with GOOS=linux: clean.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: exit 0 (runs/lint.txt).
- Linux: `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w8b-polish --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w8b-polish --out C:/.../qompack-cx-w8b-polish/plans/sdd/V6-closeout/w8b-polish/runs 92b6be37 cli-race --timeout 30m -- ./internal/cli`: exit 0. Non-root uid 10001, -race, PASS internal/cli pass=441 fail=0 skip=0, including all six new subtests (the POSIX socket-absence branch too). Artifacts: runs/cx-w8b-polish-cli-race-92b6be3-20260928T031305Z-artifacts. The Go code is identical at HEAD; only evidence follows.
- No background processes or load generators are left running. No real Claude session was run, and neither ~/.qompack nor ~/.claude was touched.

Criterion changes (with rationale)
- runDaemon's documented contract "MUST never surface a non-zero exit" gains one exception: the D36(b) refusal exits 1. The owner-delegated decision D36(b) and this task require a non-zero exit. No spawner in this build launches a daemon in a disabled project, and an older spawner does not wait for its detached child (SpawnDetached calls Process.Release), so no hook exit code changes. Every other path still returns nil.
- test/guards/qompackrevision_test.go: qompackDigest and qompackPinnedVersion are re-pinned to the v1.7 bytes and "1.7". This is the guard's own protocol for an authorized revision: re-pin in the same commit, together with the Revision log entry and the QOMPACK-ERRATA.md record. It is not a loosened check.
- SP14-M3-01 retired (see (3)). No check was skipped, weakened or regenerated.

Scope notes
- The Qompack.md revision protocol (V4-ALL-08 guard) needs four things together: the "*Design document vX.Y" line, a Revision log entry, a matching `## vX.Y` record in plans/QOMPACK-ERRATA.md, and the guard re-pin. So besides section 7.5 and the Revision log I also:
  - changed the version line to "v1.7 — six-command revision, 2026-09-27";
  - added the v1.7 errata record, plus the header lines in QOMPACK-ERRATA.md;
  - updated the guard constants.
  Without these, the revision guards fail, or the document misreports its own version. Nothing else in Qompack.md moved.

Files: C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w8b-polish/internal/cli/daemon.go, .../internal/cli/daemon_disabled_test.go, .../docs/troubleshooting.md, .../docs/cannot-do.md, .../docs/install.md, .../packaging/README.md, .../internal/checkpoint/draft.go, .../Qompack.md, .../plans/QOMPACK-ERRATA.md, .../test/guards/qompackrevision_test.go, .../plans/00-ARCHITECTURE.md, .../plans/TRACEABILITY.md, .../plans/V5-SP-14-slash-commands-and-observability.md, .../plans/sdd/V6-closeout/w8b-polish/runs/

### Commits

- 2cca561b fix(cli): refuse qompack daemon in a daemon-disabled project
- 4750b952 docs(troubleshooting): daemon refusal and the watcher's interval
- 4f52a720 docs(cannot-do): state what status shows about checkpoints
- 887a3cb1 docs(install): quote the full 2.1.280 --json help and --accept-command
- 0104ca50 docs(packaging): drop the removed checkpoint command references
- 62cfb863 docs(qompack): v1.7 counts six commands in section 7.5
- 92b6be37 docs(plans): six shipped commands and SP14-M3-01 retired
- 3e8cb08b chore(v6): record w8b-polish evidence runs

### Tests

- `go test ./internal/cli -run '^TestDaemon_RefusesWhenDaemonDisabled$' -count=1 -timeout=10m (before fix)` — FAIL as intended: all 5 subtests exited 0 after a real daemon ran 30 s (runs/daemon-disabled-red.txt)
- `go test ./internal/cli -run '^(TestDaemon_RefusesWhenDaemonDisabled|TestDaemon_RunsWhenTheMergedConfigEnablesIt)$' -count=1 -v -timeout=10m` — PASS (runs/daemon-disabled-green.txt) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/cli -count=1 -timeout=30m` — ok 57.6s
- `go test ./internal/commands -count=1 -timeout=30m` — ok 3.7s
- `go test ./internal/checkpoint -count=1 -timeout=30m` — ok 82.4s
- `go test ./test/docs -count=1 -timeout=30m` — ok
- `go test ./test/guards -count=1 -timeout=30m` — FAIL only in TestCarriedDefects_WaveReportRequiresResolution; fails before this branch (known item O4, files untouched); the Qompack revision guards PASS
- `go test ./test/guards -run '^(TestQompackChangesOnlyThroughAnAuthorizedRevision|TestQompackDeclaredVersionIsRecorded|TestQompackErrataAndRevisionLogAgree)$' -count=1 -v` — PASS <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go run ./tools/devtool plugin-validate` — OK (9 file(s), 6 command(s), 7 hook event(s))
- `go run ./tools/devtool gen-config-docs --check; go run ./tools/devtool gen-command-docs --check` — both up to date
- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./internal/cli ./internal/checkpoint (Windows and GOOS=linux)` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w8b-polish --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w8b-polish --out C:/.../w8b-polish/runs 92b6be37 cli-race --timeout 30m -- ./internal/cli` — exit 0; non-root uid 10001, -race, PASS internal/cli pass=441 fail=0 skip=0, new tests included

### Criterion changes

- runDaemon doc contract: the D36(b) disabled-project refusal is the one path that exits non-zero (1). Every other path still returns nil. Rationale: D36(b); no spawner waits for the detached child's exit status.
- test/guards qompackrevision_test.go re-pinned to Qompack.md v1.7 (digest and version), following the guard's own authorized-revision protocol.
- SP14-M3-01 retired, not satisfied (D36): its gate test was deleted with the checkpoint frontend, and it returns with any future checkpoint-now route.

### Open issues

- plans/00-ARCHITECTURE.md section 5.17's status paragraph still lists 'last checkpoint seq/size' among what /qompack:status shows. Status does not show it (see the corrected docs/cannot-do.md). This is a design-target sentence outside the command-count edits, so it was left alone.
- Inventory rows that cite the deleted TestCheckpoint_* tests still need a 'retired under D36' disposition: plans/sdd/V5-VERIFY/inventory-SP-14.md I-14.11 and plans/sdd/V6-VERIFY/inventory.md 1.14.5. Also plans/MIGRATION-EVIDENCE.md E08, which says seven commands. All outside this seat's scope.
- The live-UAT lane (coordinator/live-uat.js) must count six commands. Carried from w7b-checkpoint; not in this tree.
- internal/rehydrate/hostcap.go cites 'Qompack.md v1.6 §8.6'. That is still accurate, because §8.6 was last revised in v1.6, so it was not changed.
- test/guards TestCarriedDefects_WaveReportRequiresResolution stays red until the V6 report sets final dispositions for the carried defects. This predates this branch (O4).

### Needs the owner

- No new budget or bound numbers. The one new constant is daemonEnabledKey = "runtime.daemon.enabled", a config key name.
- Coordinator ratification (under D33): runDaemon's never-non-zero contract now has one exception, the D36(b) refusal, which exits 1 with a one-line message. That is what D36(b) and the task asked for.
- Coordinator ratification: to satisfy the V4-ALL-08 revision guard, the Qompack.md revision also changed the version line (v1.6 to v1.7), added a v1.7 record to plans/QOMPACK-ERRATA.md and re-pinned test/guards/qompackrevision_test.go (digest 726e4489..., version 1.7). This goes beyond 'section 7.5 plus revision log only', but the guard's protocol requires all of it in the same commit.

## Independent review

### review:polish: sound


## Fix seat

Not run: the reviews returned no actionable (non-nit) findings.


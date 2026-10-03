# Wave 19 loudcfg (candidate 8, D59)

Branch `closeout/w19-loudcfg`. Workflow `wf_d5ae67fb-6ab`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `5fdc35f9`

### Root cause

config.Load reports a newer settingsVersion as a Warning with a key, and LoadConfigAndReport logs it at Warn, which is what troubleshooting section 6 documents. config.LoadForCapture turns the same reset into a Violation (capture_load.go:244-246) only so the hook path saves it to state/config-violations.json. reportCaptureConfig (internal/cli/config.go) then logged every Violation with Loud, so the reset was raised to LOUD level as a side effect. That put one LOUD line per hook process into LOUD.log, which only grows and is never rotated, for as long as the project stayed on the newer config. The logging design (internal/logging/logger.go) reserves Loud for "contract violations and degradation transitions". A config that stays the same is neither, and D50's 'LOUD once, not on every compaction' ruling is the precedent.

### Summary

PRODUCT CODE CHANGED (internal/cli/config.go, hook config-report path; candidate 8 should batch it).

F-C7-C49-2: fixed so hooks log nothing to LOUD for this. In the hook path, reportCaptureConfig now logs a whole-block reset for a newer settingsVersion at Warn, with the fields LoadConfigAndReport uses ("configuration warning", key, message). It still saves the reset to state/config-violations.json, so doctor's config.violations row and self-test's config.capture row are unchanged. A reset is recognised by isVersionedReset: its key is a config.VersionedSections() path (runtime.migration or runtime.phase7). Leaf violations always carry a leaf key, so the two cannot be confused. Leaf violations (invalid values, refused gated switches) still go to LOUD, as the section 6 table documents. The running daemon still owns the loud report: reloading a changed file (reload.go:99) writes one 'daemon: config reload warning' LOUD line per change, and that line is what /qompack:status shows under recent loud lines (status only reads the daemon's own ring, so hook LOUD lines never reached status anyway). On startup the daemon logs it at Warn through LoadConfigAndReport. This needs no new constant. The test file uses captureConfigLoudHookRuns=5, which is a test count, not a product bound.

Red first: TestHookCapture_NewerSettingsVersionWarnsWithoutLoud runs the real `observe prompt` hook through Dispatch 5 times in a temporary project with logs/ and {"runtime":{"migration":{"settingsVersion":99}}}. On base a357d187 it failed with 'Should be zero, but was 5': 5 LOUD.log lines, word for word the live lane's "invalid configuration value, using default" key=runtime.migration got=<nil> want=<nil>. After the fix it passes: 0 LOUD lines, 5 warn lines in the day log, and config-violations.json still records Key runtime.migration. The control TestHookCapture_InvalidValueStaysLoud checks that an invalid leaf value is still exactly one LOUD line per hook.

F-C7-C49-3 (docs): docs/backup.md has a new paragraph after the command block. It says create, verify and restore refuse with exit 1 and 'backup: resolve configuration violations and warnings before maintenance' whenever self-test's config.capture row is not ok. That is intended (backup.go uses the same LoadForCapture violations+warnings predicate as selftest.go). It names the downgrade case and links to troubleshooting section 6. Troubleshooting section 6 'After a plugin downgrade or upgrade' gains the refusal and two ways through. The preferred one is to back up with the newer build before downgrading. The other: after a downgrade, copy the file that sets the newer settingsVersion aside, delete the block config.capture names, re-run self-test until it reads ok, then back up. That changes nothing in effect, because this build already runs that block at its defaults. Since a backup copies .qompack/config.json, that backup holds the edited file. The live evidence matches: cli/b5-b8 show the backup working once the file was moved aside. Section 6's table row now also says a running daemon reloading the changed file names it once in LOUD.log as 'daemon: config reload warning'. Section 1's 'The log file' paragraph gets one clause: a newer-settingsVersion reset is warn from hooks and commands. That edit is outside section 6 and the docs seat may touch the same paragraph; it was needed for consistency.

Two commits, code first, both carrying 'Refs: V6-VERIFY, C4.9' and no attribution trailers. Worktree clean at 5fdc35f9.

### Commits

- d00d0dbb fix(cli): log a hook's newer-settingsVersion reset at warn
- 5fdc35f9 docs(backup): say a newer settingsVersion refuses maintenance

### Tests

- `go test -p 2 ./internal/cli -run '^(TestHookCapture_NewerSettingsVersionWarnsWithoutLoud|TestHookCapture_InvalidValueStaysLoud)$' -count=1 (on base a357d187, before the fix)` — FAIL as intended: TestHookCapture_NewerSettingsVersionWarnsWithoutLoud 'Should be zero, but was 5' (5 hook LOUD lines naming runtime.migration); TestHookCapture_InvalidValueStaysLoud passed
- `go test -p 2 ./internal/cli -run '^(TestHookCapture_NewerSettingsVersionWarnsWithoutLoud|TestHookCapture_InvalidValueStaysLoud|TestHookCapture_ConfigViolationClampsAndIsRecorded)$' -count=1 -v (after fix)` — PASS (all three, incl. both ConfigViolationClampsAndIsRecorded subtests)
- `go test -p 2 -timeout=30m -count=1 ./internal/cli` — ok github.com/qompack/qompack/internal/cli 98.984s
- `go vet ./internal/cli && GOOS=linux go vet ./internal/cli` — clean
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/cli/...` — exit 0, no findings
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns` — PASS runpatterns, PASS docmarkers (exit 0)
- `go test -p 2 ./test/docs -count=1` — ok github.com/qompack/qompack/test/docs 6.447s
- `go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check` — all exit 0

### Criterion changes

- New rows in internal/cli/capture_config_loud_test.go: TestHookCapture_NewerSettingsVersionWarnsWithoutLoud (5 real observe-prompt hooks give 0 LOUD lines for runtime.migration, 5 warn lines in the day log, and config-violations.json still records it) and TestHookCapture_InvalidValueStaysLoud (an invalid leaf value is still one LOUD line per hook).

### Open issues

- Invalid leaf values still go to LOUD once per hook: a project with a persistent invalid value (for example runtime.telemetry.enabled true, or a refused gated switch) still adds one LOUD.log line per hook process, which is the same unbounded-growth pattern. The section 6 table documents 'loud in the day log' for that row, so I left it alone and did not widen this fix. A possible follow-up is to make it a transition: Loud only when the persisted config-violations.json set changes, otherwise Warn.
- state/config-violations.json goes stale: persistViolations only writes when there are violations, so after the newer block is removed nothing clears the old record. Doctor's config.violations row keeps reporting runtime.migration '(from state/config-violations.json)' as degraded. This predates the change; the docs here send the operator to self-test's config.capture (a live load), which is accurate.
- The hook-path Warn line has no location field: config.Violation does not carry a Location, unlike LoadConfigAndReport's Warn for the same reset. The daemon startup and reload lines do name the file and line.
- Section 1 edit outside section 6: one clause added to troubleshooting section 1 'The log file' paragraph; the w19 docs seat could conflict on it when merging.

## Independent review

### review:loudcfg: needs-fixes

- **minor** `docs/troubleshooting.md:534 (also internal/cli/config.go:106 comment, internal/cli/capture_config_loud_test.go:74-75 doc comment, commit d00d0dbb body)` — The new table row says a running daemon "that reloads the changed file names it once in LOUD.log". That goes beyond the evidence and the code. Every daemon start adds one more `daemon: config reload warning` LOUD line, even when config.json has not changed. A forced admin.reload adds another.
  - Evidence: internal/daemon/daemon.go:98-99 declares lastCfgMTime/lastCfgSize zero-valued, and nothing seeds them at startup (they are written only at reload.go:109-110). So the first maybeReloadConfig after start (idle tick at daemon.go:878, or session.start at handlers.go:973) sees a 'changed' file. It calls config.Load and Louds every warning (reload.go:98-99). handleAdminReload (handlers.go:1649) forces the same path. The cited live evidence shows this. store-s2/logs_LOUD.log has a second line at 19:56:47.8Z, `daemon: config reload warning` key=runtime.migration, with config.json unchanged since 19:55:34. The day log shows a fresh daemon starting at 19:56:44.7Z ('scheduler: idle work registered' plus two startup Warns), and the LOUD line comes 3 s after that. The fix still meets the task's 'at most one LOUD line per daemon' bound, but the docs, the code comment and the test comment all say 'once'.
  - Fix: Reword troubleshooting.md:534 to something like: "Each daemon names it in LOUD.log as `daemon: config reload warning`: once at its first configuration check after it starts, and again whenever the file changes or `admin.reload` forces a reload." Make the same correction in the config.go:106 comment ('each daemon's first reload, and each reload of a changed file'), the test's doc comment, and the summary's claim. If a single LOUD line per change is wanted instead, a separate product fix belongs in internal/daemon: seed lastCfgMTime/lastCfgSize from the startup load. That is outside this seat's owned files.
- **minor** `docs/troubleshooting.md:627-628 (and docs/backup.md:22-23)` — The preferred route is to back up with the newer build before downgrading. Neither doc says which build can restore that backup. docs/backup.md itself says restore 'reads content with the same build' and that a restore 'is not evidence that an older release can read newer data'. Read alone, the advice suggests the downgraded build can restore a backup the newer build took, and that is unverified (notes.txt: 'cross-version and activation checks unverified').
  - Evidence: docs/backup.md: 'reads content with the same build'; 'A restore proves only the checks it reports; it is not evidence that an older release can read newer data'; 'Human UAT and cross-version compatibility remain separate release gates.' The backup also carries the newer config.json, so restoring it with the downgraded build lands the same reset block in the destination.
  - Fix: Add one clause to the 'Before a downgrade' bullet, for example: "verify and, if needed, restore that backup with the same newer build; a restore by the downgraded build is not a supported cross-version path (see docs/backup.md)". Use the matching wording in backup.md's new paragraph.
- **nit** `docs/troubleshooting.md:629-634` — The procedure says to copy the file aside and then 'delete from it the block config.capture names'. 'It' can be read as the copy rather than the original in .qompack/. Read that way, the procedure fails: self-test never reads ok, and the kept copy loses the newer block.
  - Evidence: 'copy the file ... to a place outside `.qompack/`, delete from it the block `config.capture` names ... Then run the backup, and keep the copy for the build that understands it.'
  - Fix: Name the target: "delete that block from the original `.qompack/config.json` (not the copy)".
- **nit** `docs/troubleshooting.md:629-636` — After the documented 'after a downgrade' procedure, doctor's config.violations row still reports runtime.migration as degraded '(from state/config-violations.json)'. persistViolations only writes when violations exist, so the old record is never cleared. An operator who follows section 6 to self-test ok and then runs doctor sees a contradiction the docs do not explain. The implementer flagged this as an open issue, but the new procedure text says nothing about it.
  - Evidence: Implementer open_issues item 2. internal/cli/config.go reportCaptureConfig persists only `if len(violations) > 0`.
  - Fix: Add a sentence: doctor's config.violations row reads the persisted record and keeps naming the reset until that record is replaced; self-test's config.capture is the live answer. Alternatively, route the stale-record clearing as a follow-up finding.

## Fix seat (review resolution) — status `done`, head `21ad4320`

### Root cause

F-C7-C49-2: LoadForCapture turns a newer settingsVersion block reset into a Violation so the hook path can persist it to state/config-violations.json. reportCaptureConfig then logged every Violation with Loud, so each hook process added one line to the never-rotated LOUD.log. config.Load and LoadConfigAndReport report the same reset as a Warning, and Loud is reserved for contract violations and degradation transitions (internal/logging/logger.go). The fix is in d00d0dbb and is unchanged in this round. Review round: the wording 'the daemon names it once' was wrong. daemon.lastCfgMTime and lastCfgSize start at zero (daemon.go:98-99), so every daemon's first maybeReloadConfig treats the file as changed and Louds each config.Load warning (reload.go:98-99). A forced admin.reload (handlers.go:1649, force=true) takes the same path.

### Summary

PRODUCT CODE CHANGED (d00d0dbb, internal/cli/config.go hook config-report path; unchanged in this round; candidate 8 should batch it). The two review-round commits change only docs and comments.

Review resolution
1. Finding 1 (minor, 'names it once' overstates): CONFIRMED and fixed in 9c8a63d5.
   - Code: daemon.go:98-99 declares lastCfgMTime/lastCfgSize zero-valued, and only reload.go:109-110 writes them. So each daemon's first config check (idle tick at daemon.go:878, or session.start at handlers.go:973) sees a changed file and Louds the warning. handleAdminReload calls reloadConfigKeys(..., true).
   - Live evidence: in store-s2, a fresh daemon logs its two startup Warns at 19:56:44.6-44.7Z. A second LOUD line, 'daemon: config reload warning' key=runtime.migration, follows at 19:56:47.8Z, and config.json had not changed.
   - Rewording: the troubleshooting section 6 table row now says each running daemon names it in LOUD.log once at its first configuration check after it starts, again whenever the file changes, and again when admin.reload forces a reload. The reportCaptureConfig comment and the doc comment of TestHookCapture_NewerSettingsVersionWarnsWithoutLoud say the same.
   - d00d0dbb's commit body still says 'once'. I did not rewrite that commit; 9c8a63d5's body says it supersedes that sentence.
   - I did not seed lastCfgMTime/lastCfgSize at startup. internal/daemon is outside this seat's files, and the fix still meets the task's 'at most one LOUD line per daemon' bound (see open issues).
   - No test is red-first here: the change is wording only, with no behaviour change.
2. Finding 2 (minor, which build restores a pre-downgrade backup): CONFIRMED and fixed in 21ad4320.
   - docs/backup.md already says restore 'reads content with the same build' and 'is not evidence that an older release can read newer data'. The backup also carries the newer config.json.
   - Troubleshooting section 6's 'Before a downgrade' bullet now says to verify and, if needed, restore that backup with the same newer build. It says a restore by the downgraded build is not a supported cross-version path (with a link to backup.md), and that the restored project would hold the newer file again.
   - backup.md's new paragraph now says the same.
   - I checked internal/cli/backup.go:100-111. The config refusal applies to the source project's configuration, so the docs make no claim about the downgraded build refusing the restore itself.

What the previous round delivered still stands (see the implementer's report): hooks log the newer-settingsVersion reset at warn, it is still saved to config-violations.json, invalid leaf values are still logged LOUD, and the backup-refusal docs are in place.

Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-loudcfg is clean at 21ad4320. I tested a single pre-split commit (not kept on the branch). I then split it into 9c8a63d5 + 21ad4320 so each doc hunk sits in the commit whose message describes it; its diff against 21ad4320 was empty (coordinator note: the pre-split SHA is omitted because it is unreachable). No commit has an attribution trailer.

Incidental: the first full internal/cli run failed one test that this change does not touch. TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook failed at segment_roll_live_test.go:239: OpenSegmentTokens expected 8, actual 12, 'its open-segment account is the restored one plus the new Read'. That test uses no settingsVersion config, and the code paths changed here are only the hook's versioned-reset log level plus comments and docs. It then passed 30 of 30 runs on its own (-count=10 and -count=20), and a second full package run was ok. I am routing it to the coordinator under open issues because 12 vs 8 looks like one Read counted twice, possibly drain or replay under load.

### Commits

- d00d0dbb fix(cli): log a hook's newer-settingsVersion reset at warn
- 5fdc35f9 docs(backup): say a newer settingsVersion refuses maintenance
- 9c8a63d5 docs(troubleshooting): name each daemon's settingsVersion loud line
- 21ad4320 docs(backup): restore a pre-downgrade backup with the newer build

### Tests

- `go vet ./internal/cli && GOOS=linux go vet ./internal/cli` — clean
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/cli/...` — exit 0, no findings
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns` — PASS runpatterns, PASS docmarkers (exit 0)
- `go run ./tools/devtool gen-config-docs --check; go run ./tools/devtool gen-command-docs --check; go run ./tools/devtool gen-mcp-docs --check` — all exit 0 (config-reference.md, commands.md, mcp-tools.md up to date)
- `go test -p 2 -count=1 ./test/docs` — ok github.com/qompack/qompack/test/docs 3.325s
- `go test -p 2 -timeout=30m -count=1 ./internal/cli (run 1)` — FAIL 99.108s: only TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook (segment_roll_live_test.go:239, expected 8 actual 12), which this change does not touch; see open issues
- `go test -p 2 -count=10 -run '^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$' ./internal/cli` — ok 20.163s
- `go test -p 2 -count=20 -run '^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$' ./internal/cli` — ok 50.689s
- `go test -p 2 -timeout=30m -count=1 ./internal/cli (run 2)` — ok github.com/qompack/qompack/internal/cli 103.003s

### Criterion changes

- Unchanged from the implementer: new rows TestHookCapture_NewerSettingsVersionWarnsWithoutLoud and TestHookCapture_InvalidValueStaysLoud in internal/cli/capture_config_loud_test.go. This round changed only the first one's doc comment.

### Open issues

- Intermittent: TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook failed once in a full ./internal/cli run while other seats were loading the machine. At segment_roll_live_test.go:239 OpenSegmentTokens was 12 where 8 (recBefore.Tokens+recAfter.Tokens) was expected, which looks like one Read counted twice. It passed 30/30 on its own and in a second full run. This change does not touch it. Possibly related to the drain/replay work in the drain.go seat; the coordinator should run it under co-load (-count=20) on the base a357d187.
- Each daemon still writes one 'daemon: config reload warning' LOUD line at its first config check after start, even when config.json is unchanged, because lastCfgMTime/lastCfgSize are not seeded from the startup load (internal/daemon/daemon.go:98-99, reload.go:79-110). Every daemon restart or respawn therefore adds a line to LOUD.log for as long as a newer settingsVersion stays in force. The docs now describe this accurately. A product fix belongs in internal/daemon (seed both fields from the startup load), outside this seat's files.
- d00d0dbb's commit body says a running daemon's reload of the changed file names it 'once in LOUD.log'. 9c8a63d5's body supersedes that sentence; I did not rewrite history.
- Carried from the implementer: invalid leaf values still go to LOUD once per hook process (the same unbounded-growth pattern for a persistent invalid value or a refused gated switch). Section 6 documents that, so it was left alone. A possible follow-up: log Loud only when the persisted config-violations.json set changes.
- Carried: state/config-violations.json goes stale. persistViolations writes only when violations exist, so doctor's config.violations row keeps reporting runtime.migration after the newer block is removed. This predates this work.
- Carried: the hook-path Warn line for a versioned reset has no location field, because config.Violation carries none. The daemon startup and reload lines do have one.
- Carried: one clause was added to troubleshooting section 1's 'The log file' paragraph, outside section 6. The w19 docs seat may conflict with it on merge.


## Verify — verdict `sound`, 0 finding(s)

An independent verify seat re-checked the fix seat's head against every review finding and found nothing still wrong.

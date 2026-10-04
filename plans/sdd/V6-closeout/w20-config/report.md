# Wave 20 config (candidate 8 pre-freeze audit fixes, D61(a))

Branch `closeout/w20-config`. Workflow `wf_a18b8846-180`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **major** `internal/cli/config.go:115 and :165 (reportCaptureConfig -> persistViolations -> paths.WriteAtomic)`: This is pre-existing, not a regression from w19. While any §11.3 violation stays in force, every hook rewrites the same state/config-violations.json with an fsynced atomic write. That costs about 19 ms per hook on Windows. The cases are exactly the ones D59 targets: a newer settingsVersion after a downgrade, or a persistent invalid leaf. It puts every hook over the B-A 15 ms budget for as long as the condition lasts. w19-loudcfg removed the per-hook LOUD line but left the per-hook durable write, which is the larger cost of the same persistent condition.
- **minor** `internal/cli/config.go:111-116 (leaf violations at Loud); plans/sdd/V6-closeout/w19-loudcfg/report.md:46,127`: A same-class item has no ruling. A project with a persistent invalid leaf value or a refused gated switch still writes one LOUD.log line per hook process, into a LOUD.log that is append-only and never rotated (troubleshooting.md:303-304). D59 fixed only the newer-settingsVersion instance and cited D50's 'LOUD once' precedent. The leaf case, and each daemon start re-Louding the reset because lastCfgMTime/lastCfgSize are not seeded at startup (daemon.go:98-99), are now documented, but no ledger row accepts them.
- **minor** `internal/cli/config.go:116-117 (leaf violations still log.Loud per hook)`: This is a known open issue, documented in the §6 table, and not a regression. A persistent invalid leaf or refused gated switch still adds one LOUD.log line per hook process to a LOUD.log that is never rotated. That is the same unbounded-growth pattern F-C7-C49-2 fixed for settingsVersion, and it runs against D50's 'LOUD once' precedent. Hook LOUD lines never reach status: status reads only the daemon's ring, and the daemon already reports the condition once per start or change. So the per-hook lines buy nothing.
- **minor** `internal/cli/config.go:115 (persist only when len(violations)>0); internal/cli/doctor.go:745-757; docs/troubleshooting.md:662-668`: Following troubleshooting §6's new 'After a downgrade' procedure leaves doctor and self-test contradicting each other forever. self-test's config.capture reads ok. doctor's config.violations stays `degraded 1 leaf/leaves fell back to the default / runtime.migration (from state/config-violations.json)`, because nothing ever clears the record. The review's nit 4 asked for a sentence about this. The fix seat did not add it, and the verify seat passed without it.
- **minor** `docs/backup.md:17-19; docs/troubleshooting.md:652-655`: Both docs say backup refuses with `backup: resolve configuration violations and warnings before maintenance` whenever self-test's config.capture row is not ok. That is false when config.capture is refused (FAIL critical), for example an unparseable config.json, a runtime.redact problem or an unusable runtime.mode. There backup prints a different message, `backup: configuration unavailable: ...`, from backup.go:103-104. The exit code is 1 in both cases. troubleshooting's 'the same refusal follows any key that row names' only holds for the warn state.
- **minor** `docs/backup.md:17-20; docs/troubleshooting.md:652-656`: The docs say all three backup commands refuse with `backup: resolve configuration violations and warnings before maintenance` whenever self-test's config.capture row is not ok. When the capture loader refuses outright (row Critical, 'refused: every hook admits nothing'), backup fails with a different message.
- **minor** `docs/troubleshooting.md:561-565 and 298-300`: The config table says unknown keys, wrong types and retired meanings show as 'warn in the day log only'. The candidate 8 edit added the daemon's LOUD line to the newer-settingsVersion row alone. In fact the daemon logs every config.Load warning Loud, at its first configuration check after start and on each reload.
- **minor** `docs/troubleshooting.md:661-668 (§6 'After a downgrade'), docs/troubleshooting.md:246-256; internal/cli/config.go:84-86,111-113`: Two loudcfg review nits were left unfixed. (1) 'copy the file ... to a place outside .qompack/, delete from it the block' can be read as editing the copy, and an operator who does that never gets self-test to read ok. (2) The procedure ends at self-test ok, but doctor's config.violations row keeps reporting runtime.migration as degraded '(from state/config-violations.json)'. reportCaptureConfig returns early once nothing is wrong and persists only when violations exist, so the record is never cleared. The docs do not say so, and the operator who follows §6 meets a contradiction.
- nit `docs/troubleshooting.md:663`: Review nit 3 was not applied. 'copy the file ... to a place outside `.qompack/`, delete from it the block' still reads as editing the copy. The 'after a downgrade' bullet also never says to stop the daemon before 'Then run the backup' (the 'before' bullet does). Without that, backup refuses with `stop the source daemon before maintenance`.
- nit `internal/cli/config.go:108 and its doc comment at :79-81`: The comment says the hook path uses 'LoadConfigAndReport's levels and fields', but the hook-path Warn for a versioned reset has no location field. LoadConfigAndReport's Warn for the same reset does name file:line. This is known (config.Violation has no Location).
- nit `internal/cli/doctor.go:749`: doctor counts a whole versioned-block reset as `1 leaf/leaves fell back to the default`, but a block reset is not a leaf.
- nit `internal/cli/capture_config_loud_test.go:76-91`: The red/green row covers only runtime.migration. runtime.phase7 takes the same path through VersionedSections and has no unit row. I proved it e2e: phase7 settingsVersion 99 with 11 hooks gives LOUD.log 1 line, the daemon reload only.

## impl:config: status `done`, head `85fc00ae`

### Root cause

reportCaptureConfig and LoadConfigAndReport called persistViolations, which called paths.WriteAtomic every time the list was non-empty, with no comparison against the file on disk; nothing ever removed the file. The hook path logged §11.3 leaf violations with log.Loud once per process. The daemon's reload bookkeeping (lastCfgMTime/lastCfgSize) was never seeded at startup, so the first check reloaded the file the composition root had just loaded and Louded every warning again.

### Summary

PRODUCT CODE CHANGED. All 8 findings and all 4 nits held when I re-checked them on 738d67c7, and all are fixed on closeout/w20-config (3 commits on top of 738d67c7). Each fix has a test that fails on 738d67c7 and passes now, except the phase7 coverage row, which also passes on base (see Tests).

(1) Hook cost. reportCaptureConfig and LoadConfigAndReport now share one helper, syncViolationsRecord in internal/cli/config.go. It compares the encoded list with the file already on disk and writes only when they differ. An empty list removes the file, which costs one Lstat when there is no file. The hook path removes the record whenever its load finds no violation. LoadConfigAndReport removes it only when config.Load also reports no versioned reset: config.Load types the reset as a keyed warning, so removing on zero violations alone would let a command erase a reset a hook had just recorded.
Measured with a scratch benchmark of admitHookCapture, not committed (100x, -count 6, -cpu 4, logs/ present, Windows, machine under daytime load, so timings are indicative only):
- Clean config: 1.29-1.50 ms (median about 1.34) before, 0.66-0.87 ms (about 0.72) after.
- settingsVersion 99: 10.3-34.9 ms (median about 24.3) before, 0.97-1.18 ms (about 1.07) after.
- Invalid leaf: 20.2-89.1 ms (median about 22.9) before, 1.23-1.95 ms (about 1.70) after.
A pinned-mtime probe in the same benchmark showed the file rewritten on every hook before (rewritten=1) and never after (rewritten=0).
Operations per hook while a condition persists: before, 1 paths.WriteAtomic (2 MkdirAll, CreateTemp, write, file fsync, close, rename, directory fsync); after, 1 shared open/read/close and no write or fsync. A clean hook now costs 1 Lstat, up from 0.

(2) LOUD rules (D59: the daemon reports loudly once per start or change, hooks log warn).
- Hooks now log persistent leaf violations and refused gated switches at warn. TestHookCapture_InvalidValueStaysLoud becomes TestHookCapture_InvalidValueWarnsWithoutLoud; this is a criterion change.
- Daemon: I found that every daemon start Louded each violation twice (once from the startup LoadConfigAndReport, once from the first reload check, whose lastCfgMTime/lastCfgSize started at zero). It also promoted unchanged keyed warnings (unknown key, newer-settingsVersion reset) from warn to loud on every start.
- My decision: seed the stamp. This follows logger.go (Loud is reserved for contract violations and transitions) and LoadConfigAndReport's documented levels (keyed warning = Warn). runDaemon stats config.json before its load (daemon.StampConfigFile) and passes the result as Options.CfgStamp, which New seeds the bookkeeping from.
- Result per daemon start: one Loud per §11.3 violation, the startup report that status shows.
- Result per change or admin.reload: every warning is Loud as `daemon: config reload warning`, unchanged. C4.9 (b)'s live leg writes the file mid-session, so it still sees that line.
- An unchanged reset or unknown key is no longer Loud at each daemon start.
- If the startup load fails, no stamp is passed, so the first check retries the file. An embedder that never stamps keeps the old first check.

(3) runtime.phase7: TestHookCapture_NewerSettingsVersionWarnsWithoutLoud now runs once per config.VersionedSections() entry, so phase7 has its row and any later block gets one automatically.

(4) Docs.
- docs/backup.md and §6: the warn-state message and the critical-state message (`backup: configuration unavailable: …`) are now described separately.
- §6 'After a downgrade': keep the copy as it is and delete the block from the original; the same self-test run clears the record so doctor agrees; stop the daemon before the backup, or it refuses with `stop the source daemon before maintenance`.
- §6 table: 'warn in the day log only' corrected, plus a note on reload/start loudness and on the record being removed.
- §1 'The log file' paragraph (the finding cited lines 298-300): follows the hook's new warn level. This is the one edit outside §6 and the config table.
- doctor: '1 leaf/leaves' becomes '1 setting(s) fell back to the default'.
- Nit on the location field: the comment that claimed 'LoadConfigAndReport's levels and fields' was reworded; Violation carries no Location.

### Commits

- 660d876f fix(cli): rewrite config violations only when they change
- d4d86168 fix(daemon): seed the reload stamp from the startup load
- 85fc00ae docs(troubleshooting): match config logging and backup refusals

### Findings resolution

- **fixed**: 0 loudcfg major: every hook rewrites config-violations.json with an fsynced atomic write while a violation persists
  - Held on 738d67c7: scratch benchmark sv99 median about 24 ms, rewritten on every hook. Skeptic 2 was right that the Windows B-A budget is 50 ms, not 15, so whether the old cost breached the budget is unproven. The write was still durable churn with no content change, and fixing it was cheap. syncViolationsRecord compares the bytes first (paths.ReadFileShared); an unreadable record falls back to writing. Rows: TestHookCapture_UnchangedViolationsAreNotRewritten and TestLoadConfigAndReport_RecordFollowsTheLoad, both red on base (record replaced). The check pins the record's mtime to 2001 rather than using os.SameFile, which re-reads the file id by path on Windows. After the fix sv99 is about 1.07 ms with 0 rewrites. Commit 660d876f.
- **fixed**: 1 complete minor: leaf/gated-switch LOUD per hook and daemon re-Loud on start, no ledger row
  - Fixed in code rather than by a ledger row. Hooks log leaf violations at warn (660d876f). Daemon start no longer re-Louds an unchanged file: runDaemon stamps config.json before its load and New seeds lastCfgMTime/lastCfgSize from Options.CfgStamp (d4d86168). Red on base: TestDaemonStart_ReportsAnUnchangedConfigOnce (expected 1, actual 2 Loud lines for the violation, plus two 'daemon: config reload warning' lines) and TestHookCapture_InvalidValueWarnsWithoutLoud (5 LOUD lines from 5 hooks). The daemon-package row TestConfigReload_StartupStampSkipsTheUnchangedFile could not compile on base because the API is new; its 'no stamp' subtest reproduces base behaviour.
- **fixed**: 2 loudcfg minor: leaf violations still log.Loud per hook into never-rotated LOUD.log
  - Skeptic 2 argued the per-hook level is documented policy. The task applies D59's rule instead (the daemon reports loudly once per start or change, hooks log warn), and §11.3's 'reported through Loud, surfaced in status' still holds through the daemon's startup report and the commands that log. reportCaptureConfig now logs every class at warn. The §6 table and §1 log-file paragraph are updated. Criterion change: TestHookCapture_InvalidValueStaysLoud becomes TestHookCapture_InvalidValueWarnsWithoutLoud (invalid leaf and refused gated switch: 0 LOUD lines, N warn lines, record still written).
- **fixed**: 3 loudcfg minor: doctor config.violations stays degraded after the §6 downgrade procedure (stale record)
  - The record is now removed by any hook load with no violation, and by LoadConfigAndReport (self-test and config print included) when it finds neither a violation nor a versioned reset. Row: TestHookCapture_ResolvedConfigRemovesViolationsRecord, red on base (record kept). After the fix, doctor's config.violations reads ok after the repair, both for a clean config and for one with only an unknown key.
- **fixed**: 4 loudcfg minor: backup.md and §6 quote the warn-state message for the critical config.capture state too
  - Confirmed in backup.go:103-111: an err gives 'backup: configuration unavailable: %w', and violations or warnings give the 'resolve ... before maintenance' message. Both docs now give each state its own message. Commit 85fc00ae.
- **fixed**: 5 docs minor: same backup message mismatch (docs dimension)
  - Duplicate of finding 4; fixed by the same edit in docs/backup.md and troubleshooting §6.
- **fixed**: 6 docs minor: §6 table 'warn in the day log only' vs the daemon's Loud on start/reload
  - Fixed in both code and docs. The daemon no longer Louds an unchanged file at start (seeded stamp). The table rows drop 'only', and a note under the table states that every class is loud when a running daemon reloads a changed file or on admin.reload, that an unchanged invalid value is the once-per-start loud line, and that keyed warnings stay warn at start. §1 'The log file' (the finding's lines 298-300) was corrected too.
- **fixed**: 7 complete minor: 'After a downgrade' copy wording ambiguous; stale record unmentioned
  - The bullet now says to keep the copy unchanged and delete the block from the original, not from the copy. It says the same self-test run removes the reset's entry so doctor reads ok, and that the daemon must be stopped first or backup refuses with 'stop the source daemon before maintenance'. This also covers nit 1.
- **fixed**: nit: §6 copy wording and missing stop-daemon step
  - See finding 7.
- **fixed**: nit: comment claims 'LoadConfigAndReport's levels and fields' though the hook warn has no location
  - Comment reworded; config.Violation has no Location, so the cheaper fix the auditor offered was taken.
- **fixed**: nit: doctor '1 leaf/leaves' for a block reset
  - Now '%d setting(s) fell back to the default', matching captureConfigDegradedSummary. Pinned in TestHookCapture_ResolvedConfigRemovesViolationsRecord, red on base (actual '1 leaf/leaves fell back to the default').
- **fixed**: nit: runtime.phase7 versioned path has no unit row
  - TestHookCapture_NewerSettingsVersionWarnsWithoutLoud is table-driven over config.VersionedSections(), with subtests runtime.migration and runtime.phase7.

### Tests

- `go test -p 1 ./internal/cli -run '^(TestHookCapture_NewerSettingsVersionWarnsWithoutLoud|TestHookCapture_InvalidValueWarnsWithoutLoud|TestHookCapture_UnchangedViolationsAreNotRewritten|TestHookCapture_ResolvedConfigRemovesViolationsRecord|TestHookCapture_NoRecordNoQompackStaysUntouched|TestLoadConfigAndReport_RecordFollowsTheLoad)$' -count 1 (on base 738d67c7, new test file only)`: RED as intended: InvalidValueWarnsWithoutLoud 5 LOUD lines per subtest; UnchangedViolationsAreNotRewritten and RecordFollowsTheLoad record replaced; ResolvedConfigRemovesViolationsRecord record kept, then '1 leaf/leaves'. NewerSettingsVersion (incl. phase7) and NoRecordNoQompack passed on base (coverage rows). <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 1 ./internal/cli -run '^TestDaemonStart_ReportsAnUnchangedConfigOnce$' -count 1 (internal/cli/daemon.go reverted to 738d67c7)`: RED: expected 1 actual 2 Loud lines for runtime.telemetry.enabled, plus two 'daemon: config reload warning' lines
- `go test -p 1 ./internal/cli -run '^(TestHookCapture_NewerSettingsVersionWarnsWithoutLoud|TestHookCapture_InvalidValueWarnsWithoutLoud|TestHookCapture_UnchangedViolationsAreNotRewritten|TestHookCapture_ResolvedConfigRemovesViolationsRecord|TestHookCapture_NoRecordNoQompackStaysUntouched|TestLoadConfigAndReport_RecordFollowsTheLoad|TestDaemonStart_ReportsAnUnchangedConfigOnce)$' -count 20`: ok (47.9s) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 1 -race ./internal/cli -run '^(TestHookCapture_NewerSettingsVersionWarnsWithoutLoud|TestHookCapture_InvalidValueWarnsWithoutLoud|TestHookCapture_UnchangedViolationsAreNotRewritten|TestHookCapture_ResolvedConfigRemovesViolationsRecord|TestHookCapture_NoRecordNoQompackStaysUntouched|TestLoadConfigAndReport_RecordFollowsTheLoad|TestDaemonStart_ReportsAnUnchangedConfigOnce)$' -count 3`: ok <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 1 ./internal/daemon -run '^TestConfigReload_StartupStampSkipsTheUnchangedFile$' -count 20`: ok (on base: compile error, CfgStamp/StampConfigFile undefined)
- `go test -p 1 -race ./internal/daemon -run '^TestConfigReload_StartupStampSkipsTheUnchangedFile$' -count 3`: ok
- `go test -p 1 -timeout=30m ./internal/cli -count 1`: ok (113.6s), exit 0
- `go test -p 1 -timeout=30m ./internal/daemon -count 1`: ok (784.2s), exit 0
- `go test -p 1 ./test/docs ./test/guards -count 1`: ok / ok
- `GOOS=windows|linux|darwin go vet ./internal/cli ./internal/daemon`: clean on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/cli/... ./internal/daemon/...`: clean
- `go run ./tools/devtool fmt-check`: clean (after gofumpt -w on the test file)
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS both
- `go run ./tools/devtool gen-config-docs --check / gen-command-docs --check / gen-mcp-docs --check`: all up to date
- `go test -p 1 ./internal/cli -run (the six commit-1 rows above) -count 1 at 660d876f`: ok: commit 1 builds and passes on its own (bisectable) <!-- runpatterns: the -run argument is prose naming the six commit-1 rows listed above, not a runnable pattern -->
- `scratch BenchmarkScratchW20Admit (not committed), -benchtime 100x -count 6 -cpu 4, base vs fix`: medians: clean 1.34 -> 0.72 ms; sv99 about 24.3 -> 1.07 ms; leaf about 22.9 -> 1.70 ms; record rewritten per hook 1 -> 0
- `go run ./tools/devtool check-commit-msg <each message>`: rc=0 for all three; no line over 100 characters

### Criterion changes

- TestHookCapture_InvalidValueStaysLoud (1 LOUD line for an invalid leaf) is replaced by TestHookCapture_InvalidValueWarnsWithoutLoud (0 LOUD lines and N warn lines over N hooks, for an invalid leaf and for a refused gated switch, with the record still written). Reason: task C4.9/D59 makes hooks log a persistent configuration condition at warn and leaves the loud report to the daemon, once per start or change; the old per-hook Loud grew the never-rotated LOUD.log and never reached status.
- TestHookCapture_NewerSettingsVersionWarnsWithoutLoud now runs once per config.VersionedSections() entry (adds runtime.phase7). Its doc comment no longer claims a Loud at each daemon's first check. Reason: nit 4 and the seeded reload stamp.
- doctor's config.violations Observed text changes from '%d leaf/leaves fell back to the default' to '%d setting(s) fell back to the default'. Reason: a versioned block reset is not a leaf (nit 3).

### Open issues

- The `/qompack:` commands (status, pin and the others, via qompack_commands.go) and `qompack mcp` still call LoadConfigAndReport with a real logger. Each run therefore Louds every violation into LOUD.log. This is bounded by how often a user runs commands and predates this work; it is now documented in the §6 table, but not changed.
- Troubleshooting §1's `.qompack/state/config-violations.json` subsection does not yet say the record is removed when a load finds nothing to record; §6 says it. I left §1 alone because it is outside my named sections; it is worth one sentence from the docs owner.
- docs/config-reference.md:207-208 (generated) says a refused gated switch 'is reported as a warning', while the code records it as an invalid value. This was the skeptic's side note, it predates this work, and the generated input is not mine.
- CHANGELOG/release notes do not mention the logging change (hook violations are now warn; an unchanged file is not re-Louded at daemon start) or the removal of config-violations.json. CHANGELOG is not my file.
- docs/uat.md UAT-11's Result block (historical, candidate-era) lists LOUD.log for the settingsVersion step. It records a past run and was not edited.
- LoadConfigAndReport still writes only config.Load's violation list. So with a reset and a leaf violation both in force, a command run rewrites the record without the reset, and the next hook rewrites it with the reset. Each switch costs one write; this predates this work and is now bounded by the compare-first rule.

### Needs owner

- Decision recorded under D33 for ledger confirmation: an unchanged newer-settingsVersion reset or unknown key is no longer Loud at each daemon start; it is Loud only when a reload finds a changed file, or on admin.reload. A §11.3 violation stays Loud once per daemon start. Basis: logger.go reserves Loud for contract violations and transitions, and LoadConfigAndReport documents keyed warnings at Warn. C4.9 (b)'s live leg writes the file mid-session, so it still sees 'daemon: config reload warning' in LOUD.log and status.
- No new numeric constants in product code. Test-only: pinnedRecordTime = 2001-02-03T04:05:06Z, an mtime no write in a test run can produce, used to detect a replaced record with no clock margin.

## review:config:0:r1: verdict `needs-fixes`, 4 finding(s)

- **major** `internal/cli/config.go:232 (syncViolationsRecord: paths.ReadFileShared before the compare)`: The new compare-first read opens state/config-violations.json with no file-type or size check. Off Windows, openShared is a plain os.Open, so whenever a violation is in force a FIFO at that path blocks the hook forever, and a symlink to a device or a huge file is read without limit. That hits every hook, and also daemon start, self-test and config print, because they reach the same read through LoadConfigAndReport. On base, WriteAtomic just renamed over whatever was there. This is a regression in the rule that a hook never blocks. The config loader holds config.json to that rule already: it refuses a file that is not plain or is over its size bound.
  - Evidence: Linux probe: cross-compiled base 738d67c7 and HEAD 85fc00ae, run in the running docker-desktop WSL distro under /dev/shm. Config was {"runtime":{"telemetry":{"enabled":true}}} with mkfifo .qompack/state/config-violations.json, then one `observe prompt` hook under `timeout 15`. Base: rc=0, 0 s, record replaced by a regular file. New: rc=143 (killed by timeout after 15 s), record still a FIFO. The precondition is a special file planted in the product-owned state/ directory, so it is unlikely, but the hang lasts the host's whole hook timeout on every hook while the condition persists.
  - Fix: In syncViolationsRecord's non-empty branch, Lstat p first, as the empty branch already does. Read and compare only when fi.Mode().IsRegular() and fi.Size() == int64(len(b)), and bound the read to len(b)+1 bytes. Otherwise go straight to paths.WriteAtomic, whose rename replaces a FIFO or symlink as base did. Add a !windows test that plants a FIFO at the record path, runs one hook under a deadline, and asserts the hook returns and the record becomes a regular file. The same size check also removes the read entirely when the list length changed.
- **minor** `internal/cli/daemon.go:118-131 (CfgStamp seeding) with internal/cli/config.go:52-58; docs/troubleshooting.md:565`: Seeding the stamp removed the only loud report a persistent newer-settingsVersion reset ever had. That reset is the main D59 case. Hooks, commands and the daemon's startup load all log it at warn, and status does not read config-violations.json. After a daemon restart on an unchanged downgraded file, status shows nothing at all about it. The task gives D59's rule as 'the daemon reports the condition loudly once per start or change'. For the reset the result is now zero per start, while a §11.3 leaf gets exactly one. The seat chose this and flagged it as needs_owner, but it is a visible change in status.
  - Evidence: Probe on real binaries: project with {"runtime":{"migration":{"settingsVersion":99}}}, 3 prompt hooks, then a session-start hook, then `qompack status`. Base: status prints `loud.total 1` and the recent loud line `daemon: config reload warning key=runtime.migration ... settingsVersion 99 is newer...`. New: status prints no loud line and no config line, and LOUD.log is empty. grep shows no status code path that reads configViolationsFile, which only config.go, doctor.go and homeroot.go reference. Live C4.9 (b) still passes only because it writes the file while a daemon is running.
  - Fix: Either keep exactly one loud report per daemon start for a reset: in runDaemon, after LoadConfigAndReport, Loud each versioned-reset warning once (anyVersionedReset or isVersionedSection on warns). The seeded stamp still prevents the duplicate from the first check. Then update the §6 table row and the code comments. Or record the owner ruling (D33) that an unchanged reset is warn-only at start and that status will not show it, and say so in the §6 table row for newer settingsVersion. Do one of these before merge.
- **minor** `internal/cli/config.go:68-77 (LoadConfigAndReport persists config.ViolationsFromWarnings only)`: The two writers still encode different lists when a reset and a leaf violation are in force together. The hook writes leaves plus resets, while LoadConfigAndReport writes leaves only. Each daemon start, `qompack mcp` start, /qompack: command and self-test therefore does one fsynced WriteAtomic that drops the reset, and the next hook does another that restores it. This is the same unchanged-content durable churn that finding 0 removed. Between the two writes, doctor's config.violations row omits the reset. The seat listed it as open issue 6. It is bounded by command and daemon-start frequency, but nothing makes it necessary.
  - Evidence: Probe on the real binary with telemetry.enabled=true, maxPayloadBytes over the cap, rehydrate.maxTokens=-5, and migration and phase7 settingsVersion 99, over 10 hooks. The record's file id changed between hook 0 and hook 1 (the daemon had started in between, and its LoadConfigAndReport wrote the 3-leaf list), then stayed fixed for hooks 1-9. The hook's record holds 5 entries, the leaves first and then runtime.migration and runtime.phase7. In code, LoadConfigAndReport calls syncViolationsRecord(root, violations) with violations = ViolationsFromWarnings(warns), which never contains a reset.
  - Fix: In LoadConfigAndReport, build the persisted list as the hook does: the ViolationsFromWarnings leaves followed by a config.Violation{Key, Message} for each versioned-reset warning, in VersionedSections order. Both writers then produce identical bytes, the compare-first rule skips the write, and the `!anyVersionedReset(warns)` special case can become a plain `syncViolationsRecord(root, list)`. Extend TestLoadConfigAndReport_RecordFollowsTheLoad with a reset plus a leaf: pin the record after a hook, run one command load, and assert the record was not replaced.
- **nit** `docs/troubleshooting.md:21 and :246-253 (§1, the config-violations.json subsection)`: §1 still describes the record only as written, never removed. It tells a user who 'edited a config file and nothing changed' to 'Read the file', and says config print 'does write' it. Now any clean hook or command load removes the file, so a reader can find no file and not know that absent means nothing is in force. §6 says so, but §1 does not. The seat flagged this as outside its sections.
  - Evidence: §6 line 575: 'a load that finds no invalid value and no reset removes it'. §1 lines 246-253 and line 21 have no matching sentence. Probe: after the §6 procedure, the record no longer exists (self-test removed it).
  - Fix: Route to the docs owner: add one sentence to §1's subsection, e.g. 'The file exists only while something is in force; a load that finds no invalid value and no reset removes it (§6).' Adjust line 21's 'does write' to 'writes or removes'.

## fix:config:r1: status `done`, head `f2870cd9`

### Root cause

Round 1: persistViolations called paths.WriteAtomic every time the list was non-empty and never removed the file. Hooks logged leaf violations with log.Loud once per process. The daemon's reload stamp started at zero, so its first check re-Louded the file it had just loaded. Round 2 review: the compare-first read used paths.ReadFileShared, a plain open(2) off Windows, with no file-type or size check, so a FIFO at the record path blocked the hook. LoadConfigAndReport encoded only config.Load's leaf list, while LoadForCapture appends the versioned resets, so the two writers kept overwriting each other. The seeded stamp removed the reload-path Loud that had been a reset's only loud report, because the startup load logs keyed warnings at warn.

### Summary

PRODUCT CODE CHANGED. closeout/w20-config is now 5 commits on top of 738d67c7. All 8 audit findings and all 4 audit nits are fixed (round 1, commits 660d876f, d4d86168 and 85fc00ae). All 3 review findings and the review nit held when I re-checked them, and all are fixed in round 2 (07ff04ad, code; f2870cd9, docs). Each round-2 fix has a row that fails on 85fc00ae and passes now.

Round 1, in brief:
- (1) syncViolationsRecord compares the record before writing and removes it when nothing is in force. Measured with a scratch benchmark: settingsVersion-99 hook about 24.3 ms down to 1.07 ms, invalid leaf about 22.9 ms down to 1.70 ms, rewrites per hook 1 down to 0.
- (2) Hooks log leaf violations, gated switches and resets at warn. runDaemon stamps config.json before its load (Options.CfgStamp), so the first reload check does not re-Loud an unchanged file.
- (3) A runtime.phase7 row, table-driven over config.VersionedSections().
- (4) backup.md and §6 now give the critical-state message and the warn-state message separately. §6's downgrade steps reworded (copy, delete from the original, stop the daemon first). The table's 'warn only' claim corrected. doctor now says 'setting(s)'.

## Review resolution

**Review 1 (major): FIFO hang on the compare-first read.** It held. On Linux, the new row hung on 85fc00ae: the watchdog fired after 30 s. On 738d67c7 the unconditional rename would not have blocked, so round 1 introduced this regression.
- Fix: a new recordHolds opens the record with paths.OpenSharedLeaf. That open never follows a link and, off Windows, never blocks, so a FIFO opens at once; it is the same open config.json uses.
- It reads only a regular file whose size equals the new encoding, bounded to len+1 bytes. Anything else (link, FIFO, device, directory, other size) is replaced by paths.WriteAtomic, as on base.
- A single open, so there is no window between a check and the read.
- Rows: TestHookCapture_FIFORecordDoesNotBlockTheHook (unix-only, runs the real hook) and TestRecordHolds_OnlyAPlainFileWithTheSameBytes (all platforms).

**Review 2 (minor): seeding the stamp left a persistent reset with no loud report.** It held. TestDaemonStart_ReportsAnUnchangedConfigOnce, extended with a migration settingsVersion of 99, got 0 Loud lines on 85fc00ae where it expects 1.
- I took the reviewer's first option. The task states D59 as 'the daemon reports the condition loudly once per start or change', and the reset is D59's own case.
- runDaemon now loads through a new loadDaemonConfig, which Louds each reset once as `configuration block reset to defaults`. Unknown keys stay warn at start.
- Commands, `qompack mcp` and self-test keep LoadConfigAndReport, which logs a reset at warn. The seeded stamp still stops the first reload check from repeating the line.
- The row also checks that the line reaches logging.LastLoud(), the ring status prints its recent loud lines from (daemon/metrics.go loudTail). It reads only the lines after a marker of its own, so -count repetitions cannot satisfy it.
- This reverses round 1's needs_owner item that said an unchanged reset was warn-only at start.

**Review 3 (minor): the two writers encoded different lists.** It held. Extended TestLoadConfigAndReport_RecordFollowsTheLoad (three leaves plus migration and phase7 resets; hook, pin, command load) on 85fc00ae: 'record replaced'.
- Fix: a new recordedViolations gives config.Load's result the hook's shape: the leaves, then Violation{Key, Message} for each reset, in VersionedSections order.
- LoadConfigAndReport now calls syncViolationsRecord with that list unconditionally. anyVersionedReset and its special case are deleted.
- The row asserts that neither the command load nor the next hook rewrites the record, and that the bytes are identical.

**Review nit: §1 never said the record is removed.** Fixed, and it had to be. Review 3's fix made two statements in §1's config-violations.json subsection false: 'written here by the hook path only' and 'The second, written by the hook path only'.
- I corrected both and added the sentence that the file exists only while something is in force, so a missing file means nothing is recorded.
- The header note (line 21) now says config print writes or removes the record.
- These are the only edits outside §6 and the config table. They are confined to the record's own subsection and the header sentence, and both were made wrong by this seat's product change.

**Hook cost after round 2 (operation counts, not re-timed).**
- While a condition persists: 1 OpenSharedLeaf, 1 fstat, 1 read bounded to len+1, 1 close; no write and no fsync.
- On Windows the open is one CreateFile plus one GetFileInformationByHandleEx.
- A size change now skips the read entirely.
- A clean hook: 1 Lstat.

### Commits

- 660d876f fix(cli): rewrite config violations only when they change
- d4d86168 fix(daemon): seed the reload stamp from the startup load
- 85fc00ae docs(troubleshooting): match config logging and backup refusals
- 07ff04ad fix(cli): bound the violations read and match both writers
- f2870cd9 docs(troubleshooting): describe the reset's start report

### Findings resolution

- **fixed**: 0 loudcfg major: every hook rewrites config-violations.json with an fsynced atomic write while a violation persists
  - Round 1 (660d876f): syncViolationsRecord compares before writing. Scratch benchmark: sv99 median about 24.3 ms down to 1.07 ms, 0 rewrites. Round 2 (07ff04ad): the compare read is now recordHolds (no-follow, non-blocking, regular files of matching size only, bounded read). Rows: TestHookCapture_UnchangedViolationsAreNotRewritten, TestLoadConfigAndReport_RecordFollowsTheLoad.
- **fixed**: 1 complete minor: leaf/gated-switch LOUD per hook and daemon re-Loud on start, no ledger row
  - Hooks log at warn (660d876f). The seeded CfgStamp stops the duplicate Loud at daemon start (d4d86168). Round 2 makes a newer-settingsVersion reset Loud once per start through loadDaemonConfig (07ff04ad). Rows: TestHookCapture_InvalidValueWarnsWithoutLoud, TestDaemonStart_ReportsAnUnchangedConfigOnce, TestConfigReload_StartupStampSkipsTheUnchangedFile.
- **fixed**: 2 loudcfg minor: leaf violations still log.Loud per hook into never-rotated LOUD.log
  - reportCaptureConfig logs every class at warn; the loud report belongs to the daemon (once per start, once per change). Criterion change recorded: TestHookCapture_InvalidValueStaysLoud becomes TestHookCapture_InvalidValueWarnsWithoutLoud.
- **fixed**: 3 loudcfg minor: doctor config.violations stays degraded after the §6 downgrade procedure (stale record)
  - Any load with nothing in force removes the record. Since round 2, LoadConfigAndReport records leaves plus resets, so it removes the record exactly when the hook would. Row: TestHookCapture_ResolvedConfigRemovesViolationsRecord (doctor reads ok afterwards).
- **fixed**: 4 loudcfg minor: backup.md and §6 quote the warn-state message for the critical config.capture state too
  - 85fc00ae: both docs now give 'backup: configuration unavailable: ...' (critical) and the 'resolve ... before maintenance' message (warn) separately, matching backup.go:103-111.
- **fixed**: 5 docs minor: same backup message mismatch (docs dimension)
  - Duplicate of finding 4; fixed by the same edit.
- **fixed**: 6 docs minor: §6 table 'warn in the day log only' vs the daemon's Loud on start/reload
  - 85fc00ae corrected the table and §1's log-file paragraph. f2870cd9 updates the newer-settingsVersion row and the note under the table for round 2's start report: loud once per daemon start, which is how status shows it.
- **fixed**: 7 complete minor: 'After a downgrade' copy wording ambiguous; stale record unmentioned
  - 85fc00ae: keep the copy as it is, delete the block from the original, stop the daemon first; the same self-test run clears the record.
- **fixed**: nit: §6 copy wording and missing stop-daemon step
  - See finding 7.
- **fixed**: nit: comment claims 'LoadConfigAndReport's levels and fields' though the hook warn has no location
  - Comment reworded (660d876f); config.Violation has no Location.
- **fixed**: nit: doctor '1 leaf/leaves' for a block reset
  - Now '%d setting(s) fell back to the default'; pinned in TestHookCapture_ResolvedConfigRemovesViolationsRecord.
- **fixed**: nit: runtime.phase7 versioned path has no unit row
  - TestHookCapture_NewerSettingsVersionWarnsWithoutLoud runs once per config.VersionedSections() entry (runtime.migration, runtime.phase7).
- **fixed**: review major: syncViolationsRecord compare read (paths.ReadFileShared) blocks on a FIFO and reads unbounded
  - Held. Red on 85fc00ae in WSL Linux (docker-desktop distro, /dev/shm): TestHookCapture_FIFORecordDoesNotBlockTheHook fired its watchdog after 30.03 s ('the hook blocked on a FIFO'). Fix (07ff04ad): recordHolds uses paths.OpenSharedLeaf (O_NOFOLLOW|O_NONBLOCK off Windows; reparse-aware on Windows), then f.Stat() must be regular with Size == len(want), then reads at most len(want)+1 bytes. Anything else falls through to WriteAtomic, which renames over the FIFO as base did. Green on Linux at count 20 (0.00 s each). Cross-platform row: TestRecordHolds_OnlyAPlainFileWithTheSameBytes.
- **fixed**: review minor: seeded stamp removed the only loud report of a persistent newer-settingsVersion reset (status shows nothing after restart)
  - Held. Red on 85fc00ae: TestDaemonStart_ReportsAnUnchangedConfigOnce with migration settingsVersion 99 got 0 Loud lines where it expects 1. Took the reviewer's first option, per the task's statement of D59 ('the daemon reports the condition loudly once per start or change'). runDaemon calls the new loadDaemonConfig, which Louds each versioned reset as 'configuration block reset to defaults'; unknown keys stay warn; commands and mcp keep warn. The row also asserts the line reaches logging.LastLoud(), which status reads via daemon/metrics.go loudTail. §6 table, §1 log paragraph and code comments updated (f2870cd9).
- **fixed**: review minor: LoadConfigAndReport persisted leaves only while hooks persist leaves plus resets (write churn per command/daemon start)
  - Held. Red on 85fc00ae: extended TestLoadConfigAndReport_RecordFollowsTheLoad (telemetry.enabled, softFloorPct 9.5, minSessions -3, migration and phase7 settingsVersion 99; hook, pin, command load) failed with 'a command load writes the same list a hook recorded, so it is not rewritten'. Fix: recordedViolations appends Violation{Key, Message} per reset warning after the leaves. Both writers now produce identical bytes (asserted), and anyVersionedReset and its special case are deleted.
- **fixed**: review nit: troubleshooting §1 never says the record is removed; line 21 says config print 'does write'
  - f2870cd9. Round 2's fix made two §1 statements false ('written here by the hook path only' twice), so the subsection had to change. It now says both writers record resets, and that the file exists only while something is in force, so a missing file means nothing is recorded. Line 21 now says 'does write, or remove'. These edits stay inside §1's record subsection and the header note.

### Tests

- `go test -p 1 ./internal/cli -run '^(TestLoadConfigAndReport_RecordFollowsTheLoad|TestDaemonStart_ReportsAnUnchangedConfigOnce)$' -count 1 (extended rows on 85fc00ae)`: RED as intended: RecordFollowsTheLoad 'a command load writes the same list a hook recorded, so it is not rewritten'; DaemonStart expected 1 actual 0 Loud lines for the reset <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `GOOS=linux CGO_ENABLED=0 go test -c ./internal/cli, run in WSL docker-desktop: -test.run '^TestHookCapture_FIFORecordDoesNotBlockTheHook$' -test.count 1 (on 85fc00ae)`: RED: watchdog fired after 30.03s, 'the hook blocked on a FIFO at state/config-violations.json'
- `same Linux binary at the fix: -test.run '^TestHookCapture_FIFORecordDoesNotBlockTheHook$' -test.count 20`: PASS 20/20, 0.00s each
- `Linux binary at f2870cd9 code: -test.run '^(TestHookCapture_FIFORecordDoesNotBlockTheHook|TestRecordHolds_OnlyAPlainFileWithTheSameBytes|TestLoadConfigAndReport_RecordFollowsTheLoad|TestDaemonStart_ReportsAnUnchangedConfigOnce|TestHookCapture_NewerSettingsVersionWarnsWithoutLoud|TestHookCapture_InvalidValueWarnsWithoutLoud|TestHookCapture_UnchangedViolationsAreNotRewritten|TestHookCapture_ResolvedConfigRemovesViolationsRecord|TestHookCapture_NoRecordNoQompackStaysUntouched)$' -test.count 20 -test.v`: PASS: 180 top-level passes (9 x 20), rc=0
- `go test -p 1 ./internal/cli -run '^(TestRecordHolds_OnlyAPlainFileWithTheSameBytes|TestLoadConfigAndReport_RecordFollowsTheLoad|TestDaemonStart_ReportsAnUnchangedConfigOnce|TestHookCapture_NewerSettingsVersionWarnsWithoutLoud|TestHookCapture_InvalidValueWarnsWithoutLoud|TestHookCapture_UnchangedViolationsAreNotRewritten|TestHookCapture_ResolvedConfigRemovesViolationsRecord|TestHookCapture_NoRecordNoQompackStaysUntouched|TestCLI_ConfigViolationsAreLoudAndPersisted)$' -count 20 (Windows)`: ok (22.8s) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 1 -race ./internal/cli -run '^(TestRecordHolds_OnlyAPlainFileWithTheSameBytes|TestLoadConfigAndReport_RecordFollowsTheLoad|TestDaemonStart_ReportsAnUnchangedConfigOnce|TestHookCapture_NewerSettingsVersionWarnsWithoutLoud|TestHookCapture_InvalidValueWarnsWithoutLoud|TestHookCapture_UnchangedViolationsAreNotRewritten|TestHookCapture_ResolvedConfigRemovesViolationsRecord|TestHookCapture_NoRecordNoQompackStaysUntouched|TestCLI_ConfigViolationsAreLoudAndPersisted)$' -count 3 (Windows)`: ok (6.95s) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 1 -timeout=30m ./internal/cli -count 1`: ok (120.3s), exit 0
- `round 1: go test -p 1 -timeout=30m ./internal/daemon -count 1; go test -p 1 ./internal/daemon -run '^TestConfigReload_StartupStampSkipsTheUnchangedFile$' -count 20 and -race -count 3`: ok (784.2s) / ok / ok; internal/daemon unchanged in round 2
- `go test -p 1 ./test/docs ./test/guards -count 1`: ok / ok
- `GOOS=windows|linux|darwin go vet ./internal/cli`: clean on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/cli/... (plus the same pinned binary with GOOS=linux, covering the unix-only test file)`: clean / clean
- `go run ./tools/devtool fmt-check`: clean (pinned gofumpt v0.8.0 applied to the new unix test file)
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS both
- `go run ./tools/devtool gen-config-docs --check / gen-command-docs --check / gen-mcp-docs --check`: all up to date
- `go run ./tools/devtool check-commit-msg <07ff04ad and f2870cd9 messages>`: rc=0 both; no attribution trailers

### Criterion changes

- Round 1: TestHookCapture_InvalidValueStaysLoud (1 LOUD line) is replaced by TestHookCapture_InvalidValueWarnsWithoutLoud (0 LOUD lines, N warn lines over N hooks, record still written). Reason: C4.9/D59, hooks log persistent conditions at warn.
- Round 1: TestHookCapture_NewerSettingsVersionWarnsWithoutLoud runs per config.VersionedSections() entry (adds runtime.phase7). Round 2 changes its doc comment only, to name the daemon's start report.
- Round 1: doctor config.violations Observed text changes from '%d leaf/leaves fell back to the default' to '%d setting(s) fell back to the default'. Reason: a block reset is not a leaf.
- Round 2 (tightened): TestDaemonStart_ReportsAnUnchangedConfigOnce adds migration settingsVersion 99 to its config. It now also requires exactly 1 'configuration block reset to defaults' line for key=runtime.migration in LOUD.log, and exactly 1 in the Loud ring after the test's own marker. Reason: review finding 2 and D59 (loud once per daemon start).
- Round 2 (tightened): TestLoadConfigAndReport_RecordFollowsTheLoad adds a reset-plus-leaves case. After a hook, a command load and the next hook must not replace the record, and the bytes must be identical. Reason: review finding 3.
- Round 2 (new rows): TestHookCapture_FIFORecordDoesNotBlockTheHook (unix) and TestRecordHolds_OnlyAPlainFileWithTheSameBytes. Reason: review finding 1.

### Open issues

- Carried from round 1: the /qompack: commands (qompack_commands.go) and `qompack mcp` still call LoadConfigAndReport with a real logger, so each run Louds every §11.3 leaf violation into LOUD.log. A reset is warn on those paths. This predates this work and is documented in the §6 table.
- Carried from round 1: docs/config-reference.md:207-208 (generated) says a refused gated switch 'is reported as a warning', but the code records it as an invalid value. The generator input is not mine.
- Carried from round 1, scope grown in round 2: CHANGELOG/release notes do not mention the logging changes (hooks now warn; an unchanged file is not re-Louded at daemon start; a reset is Loud once per daemon start as 'configuration block reset to defaults'), the removal of config-violations.json, or that commands now record resets in it. CHANGELOG is not my file.
- Carried from round 1: docs/uat.md UAT-11's historical Result block lists LOUD.log for the settingsVersion step; it records a past run and was not edited.
- Closed in round 2: round 1's open issue 6 (the writers' differing lists) and round 1's open issue 2 (the §1 removal sentence).
- New, predates this work: doctor's config.violations counts a leaf twice when it is both in doctor's live load and in the persisted record, because live and persisted are not de-duplicated by key. doctorPersistedViolations also still reads the record with an unbounded paths.ReadFileShared, so a FIFO there would hang doctor (not hooks). The next hook now replaces such a FIFO with a plain file.
- New, a side effect of review fix 3: a command load whose newer-settingsVersion reset comes from the user-global, env or --set layer, in a project with no .qompack, now creates .qompack/state/config-violations.json. Base already did this for a leaf violation. A reset from the project's own config implies .qompack exists.
- Unverified: the unix-only FIFO row ran without -race on Linux. A cross-compiled -race binary needs cgo and a Linux C toolchain, which this Windows host lacks. Hosted CI's Linux -race lane will cover it.

### Needs owner

- Decision under D33, reversing round 1's item: a newer-settingsVersion block reset is Loud once per daemon start (loadDaemonConfig, message 'configuration block reset to defaults'), so status shows it after a restart on an unchanged downgraded file. An unknown key, wrong type or retired meaning stays warn at start. Every class is Loud on a reload of a changed file or on admin.reload. Hooks, commands and mcp log a reset at warn. Basis: the task's statement of D59 ('the daemon reports the condition loudly once per start or change'), and the review finding that the reset otherwise had no loud report at all.
- Test-only constant fifoHangWatchdog = 30s (internal/cli/capture_config_fifo_unix_test.go). It is a hang detector, not a performance bound: the fixed path makes no blocking open and returns in one hook's time (0.00s observed on Linux, 20/20). It is reached only by a regression, and then it frees the blocked reader. It matches the existing store watchdog convention (gc_retention_read_unix_test.go).
- Carried: test-only pinnedRecordTime = 2001-02-03T04:05:06Z, an mtime no write during a test can produce, used to detect a replaced record without a clock margin. No new numeric constants in product code.

## review:config:0:r2: verdict `needs-fixes`, 2 finding(s)

- **minor** `internal/cli/config.go:108-116 (recordedViolations) and config.go:74-76 (loadConfigAndReport's loudResets case)`: Round 2 introduced a regression. Both new branches treat any warning whose Key equals a versioned section's path (isVersionedSection) as a newer-settingsVersion reset. config.Load's deepMerge also emits warnings keyed by that exact path, for example Warning{Key:"runtime.migration", Message:"expected an object"} for a wrong-type section (load.go:400), and an unparseable --set aimed at the section would do the same. For such a config: (a) every command load (config print, self-test, the /qompack: commands, mcp, daemon start) writes a record holding a Violation that is not a §11.3 violation. (b) LoadForCapture puts that warning in warnings rather than violations, so every hook removes the record again. That brings back the write/remove churn review finding 3 was meant to end: one fsynced WriteAtomic per command or daemon start and one remove per hook. (c) Whether doctor's config.violations says degraded ('1 setting(s) fell back to the default') or ok depends on which process ran last. (d) Each daemon start Louds 'configuration block reset to defaults' into the never-rotated LOUD.log for a block that was not reset. (e) The new troubleshooting §1 text ('Both write the same list') and the existing line 289 ('values of the wrong type ... never appear in this file') are both false for this input.
  - Evidence: Real-binary probe (scratchpad probe1.sh: HOME/USERPROFILE redirected to a temp dir, QOMPACK_RUNTIME__DAEMON__ENABLED=false), project .qompack/config.json = {"runtime":{"migration":5}}. HEAD f2870cd9: after config print, record present [{"Key":"runtime.migration","Message":"expected an object","Got":null,"Want":null}]; after hook 1, no record; after hook 2, no record; after config print 2, record present with a new file id; doctor config.violations = degraded. Base 738d67c7, same probe: no record at any step, doctor config.violations = ok. In an earlier run of the same config with the daemon enabled, LOUD.log got `level=loud msg="configuration block reset to defaults" key=runtime.migration message="expected an object"` from the daemon's start load.
  - Fix: Identify a reset by its own shape, not by its key alone. In internal/config, export a predicate that matches applyVersionedSections' warning exactly: the key is a VersionedSections path AND the message has the reset form 'settingsVersion %v is newer than this build understands'. An alternative is a VersionedResetsFromWarnings decoder, as ViolationsFromWarnings does for leaves; having applyVersionedSections build its message from a shared prefix constant keeps the two from drifting. Use it in recordedViolations and in the loudResets case. Add a row: config {"runtime":{"migration":5}}. A command load creates no record, a hook leaves none, and loadDaemonConfig writes no 'configuration block reset to defaults' line (the warning stays a Warn).
- **nit** `internal/cli/doctor.go:742-752 (configViolationsRow)`: The doctor row this seat reworded and pinned still counts one setting twice once a record exists. live comes from doctor's own config.Load and persisted from the record, and they are not de-duplicated by key. A single invalid leaf therefore reads '2 setting(s) fell back to the default', which is the miscount the wording fix was meant to make readable. This predates the work (base printed '2 leaf/leaves'), and the seat disclosed it as an open issue, but the new wording makes the double count more visible.
  - Evidence: HEAD binary, project config {"runtime":{"rehydrate":{"maxTokens":-5}}} after 4 hooks: `config.violations degraded  2 setting(s) fell back to the default` with detail `runtime.rehydrate.minTokens (got 8000, want <= -5); runtime.rehydrate.minTokens (from state/config-violations.json)`.
  - Fix: Skip a persisted entry whose Key already appears in live, and count the de-duplicated set. Extend TestHookCapture_ResolvedConfigRemovesViolationsRecord, or add a row, so that one leaf in both sources reads '1 setting(s)'.

## fix:config:r2: status `done`, head `d64129b2`

### Root cause

Round 1: persistViolations called WriteAtomic unconditionally whenever the list was non-empty, and never removed the file. Hooks Louded leaf violations once per process. The daemon's reload stamp started at zero, so its first check re-Louded the file it had just loaded. Round 2: the compare read used a plain blocking open with no file-type or size check. The two writers encoded different lists. The seeded stamp removed a reset's only loud report. Round 3: the round-2 fixes identified a reset by Key == versioned section path. config.Load's deepMerge also emits a warning with that exact key for a block that is not an object, so that warning was recorded as a §11.3 setting by command loads, removed by hooks, and Louded as a reset at daemon start. Separately, doctor's configViolationsRow concatenated the live and persisted lists without de-duplicating them by key.

### Summary

PRODUCT CODE CHANGED. closeout/w20-config now has 7 commits on top of 738d67c7. All 8 audit findings and all 4 audit nits are fixed (round 1: 660d876f, d4d86168, 85fc00ae). All 3 round-2 review findings and the round-2 review nit are fixed (07ff04ad, f2870cd9). In round 3, the review finding and the review nit both held when I re-checked them, and both are fixed (ff0977c0, d64129b2). Each round-3 fix has a row that fails on f2870cd9 and passes now.

Rounds 1 and 2, in brief:
- Hooks compare the record before writing it and remove it when nothing is wrong. The hook cost while a condition persists fell from about 24 ms to about 1-2 ms, with 0 rewrites per hook.
- Hooks log every config class at warn. The daemon stamps config.json before its startup load, so its first reload check does not repeat the report. A newer-settingsVersion reset is Loud once per daemon start.
- The compare read is no-follow, non-blocking and bounded, so a FIFO at the record path no longer blocks a hook.
- Both writers (command loads and hooks) record the same list, so neither rewrites the other's record.
- runtime.phase7 has its unit row.
- backup.md and troubleshooting §6 and §1 were corrected.

## Review resolution (round 3)

**Review finding (minor): a wrong-type versioned block was treated as a reset.** It held.
- Cause: config.Load's deepMerge keys its `expected an object` warning by the block's own path (load.go:400). A reset carries the same key. recordedViolations and loadConfigAndReport's daemon-start escalation identified a reset by that key alone (isVersionedSection(w.Key)).
- Red on f2870cd9: the new row TestLoadConfigAndReport_WrongTypeBlockIsNotAReset (one subtest per VersionedSections entry; config `{"runtime":{"migration":5}}` and the same for phase7) failed all three ways.
  - loadDaemonConfig Louded `configuration block reset to defaults` once (expected 0).
  - The warning was not logged at warn.
  - A command load created the record. I confirmed each failure separately by commenting out the earlier assertions in turn.
- Fix (ff0977c0): config.Warning gets a VersionedReset bool, set only by applyVersionedSection. This uses a typed field rather than parsing message text, following the existing Deprecated field.
  - recordedViolations and the `loudResets` case now select on w.VersionedReset.
  - No test builds a config.Warning literal or compares Warning structs, so nothing else changes.
  - A wrong-type block is now an ordinary keyed warning: none of the three loads records it, and the daemon logs it at warn on start.
- Unit rows in internal/config:
  - New: TestLoad_WrongTypeVersionedBlockIsNotAReset (both blocks wrong-typed; VersionedReset false).
  - Extended: TestLoad_NewerSettingsVersionResetsTheWholeBlock now asserts VersionedReset true on the reset and false on the unknown key. The new field did not exist on base, so these rows do not compile there.
- Real-binary check of the reviewer's case: a project config of `{"runtime":{"migration":5}}`, HOME and USERPROFILE redirected to a scratch dir, daemon disabled. `qompack config print` created no state/ at all. doctor config.violations = `ok none`, which matches the reviewer's result on base 738d67c7.
- Docs: no edit needed. Once the code was fixed, troubleshooting §1 ('Both write the same list') and line 289 ('values of the wrong type ... never appear in this file') became true again. The §6 table row at line 565 already lists 'a section that is not an object' as warn in the day log.

**Review nit: doctor counted a setting twice.** It held.
- Red on f2870cd9: new row TestDoctor_ViolationInBothSourcesCountsOnce got `2 setting(s) fell back to the default`, with detail `runtime.telemetry.enabled (got true, want false); runtime.telemetry.enabled (from state/config-violations.json)`.
- Fix (d64129b2): configViolationsRow skips a persisted entry whose key is already in the live list, and also skips duplicates within the record. The live entry, which carries got and want, is the one shown. The row now reads `1 setting(s)`, names the key once, and has no '(from state/...)' suffix. A reset, which only the record holds, still shows from the record (TestHookCapture_ResolvedConfigRemovesViolationsRecord is unchanged and green).

The doctor FIFO read (doctorPersistedViolations uses unbounded paths.ReadFileShared) stays open. It was not raised in this review. It predates this work. doctor's other state reads have the same shape, so a one-off fix there would be piecemeal.

### Commits

- 660d876f fix(cli): rewrite config violations only when they change
- d4d86168 fix(daemon): seed the reload stamp from the startup load
- 85fc00ae docs(troubleshooting): match config logging and backup refusals
- 07ff04ad fix(cli): bound the violations read and match both writers
- f2870cd9 docs(troubleshooting): describe the reset's start report
- ff0977c0 fix(config): mark a versioned reset instead of keying on its path
- d64129b2 fix(cli): count a doctor config violation once

### Findings resolution

- **fixed**: 0 loudcfg major: every hook rewrites config-violations.json with an fsynced atomic write while a violation persists
  - Round 1 (660d876f): compare before writing (sv99 hook about 24.3 ms down to 1.07 ms, 0 rewrites). Round 2 (07ff04ad): recordHolds does a no-follow, non-blocking, bounded read. Round 3 (ff0977c0): a wrong-type block no longer causes write/remove churn between command loads and hooks. Rows: TestHookCapture_UnchangedViolationsAreNotRewritten, TestLoadConfigAndReport_RecordFollowsTheLoad, TestLoadConfigAndReport_WrongTypeBlockIsNotAReset.
- **fixed**: 1 complete minor: leaf/gated-switch LOUD per hook and daemon re-Loud on start, no ledger row
  - Hooks log at warn (660d876f). The seeded CfgStamp stops the duplicate Loud at daemon start (d4d86168). A real newer-settingsVersion reset is Loud once per start (07ff04ad); since ff0977c0 only a real reset (Warning.VersionedReset) is escalated. Rows: TestHookCapture_InvalidValueWarnsWithoutLoud, TestDaemonStart_ReportsAnUnchangedConfigOnce, TestConfigReload_StartupStampSkipsTheUnchangedFile, TestLoadConfigAndReport_WrongTypeBlockIsNotAReset.
- **fixed**: 2 loudcfg minor: leaf violations still log.Loud per hook into never-rotated LOUD.log
  - reportCaptureConfig logs every class at warn; the daemon owns the loud report. Criterion change: TestHookCapture_InvalidValueStaysLoud becomes TestHookCapture_InvalidValueWarnsWithoutLoud.
- **fixed**: 3 loudcfg minor: doctor config.violations stays degraded after the §6 downgrade procedure (stale record)
  - Any load with nothing in force removes the record; both writers record the same list. Row: TestHookCapture_ResolvedConfigRemovesViolationsRecord.
- **fixed**: 4 loudcfg minor: backup.md and §6 quote the warn-state message for the critical config.capture state too
  - 85fc00ae: the critical-state message ('backup: configuration unavailable: ...') and the warn-state message are now given separately, matching backup.go:103-111.
- **fixed**: 5 docs minor: same backup message mismatch (docs dimension)
  - Duplicate of finding 4; fixed by the same edit.
- **fixed**: 6 docs minor: §6 table 'warn in the day log only' vs the daemon's Loud on start/reload
  - 85fc00ae and f2870cd9 corrected the table, §1 and the note under the table. After ff0977c0 the 'section that is not an object' row (warn in the day log) is accurate again.
- **fixed**: 7 complete minor: 'After a downgrade' copy wording ambiguous; stale record unmentioned
  - 85fc00ae: keep the copy as it is, delete the block from the original, stop the daemon first; the same self-test run clears the record.
- **fixed**: nit: §6 copy wording and missing stop-daemon step
  - See finding 7.
- **fixed**: nit: comment claims 'LoadConfigAndReport's levels and fields' though the hook warn has no location
  - Comment reworded (660d876f).
- **fixed**: nit: doctor '1 leaf/leaves' for a block reset
  - Now '%d setting(s) fell back to the default'. Since d64129b2 a setting is counted once (see the round-3 nit).
- **fixed**: nit: runtime.phase7 versioned path has no unit row
  - TestHookCapture_NewerSettingsVersionWarnsWithoutLoud runs once per config.VersionedSections() entry.
- **fixed**: round-2 review major: syncViolationsRecord compare read blocks on a FIFO and reads unbounded
  - 07ff04ad: recordHolds uses paths.OpenSharedLeaf, accepts only a regular file of the expected size, and reads at most len+1 bytes. Red on 85fc00ae on Linux (30.03 s watchdog); green 20/20 on Linux. Rows: TestHookCapture_FIFORecordDoesNotBlockTheHook, TestRecordHolds_OnlyAPlainFileWithTheSameBytes.
- **fixed**: round-2 review minor: seeded stamp removed the only loud report of a persistent newer-settingsVersion reset
  - 07ff04ad: loadDaemonConfig Louds a reset once per daemon start, and the row asserts it reaches LastLoud. Narrowed in round 3 to real resets only (ff0977c0).
- **fixed**: round-2 review minor: LoadConfigAndReport persisted leaves only while hooks persist leaves plus resets
  - 07ff04ad: recordedViolations makes both writers produce identical bytes. Round 3 (ff0977c0) fixes the regression this introduced for wrong-type blocks.
- **fixed**: round-2 review nit: troubleshooting §1 never says the record is removed
  - f2870cd9: §1 says the file exists only while something is in force.
- **fixed**: round-3 review minor: recordedViolations and loudResets treat any warning keyed by a versioned section path as a reset (wrong-type block recorded by commands, removed by hooks, Louded at daemon start)
  - Held. Red on f2870cd9: TestLoadConfigAndReport_WrongTypeBlockIsNotAReset (migration and phase7 subtests) failed on the Loud count (1, expected 0), on the warn level, and on the command-load record (each confirmed separately). Fix ff0977c0: new config.Warning.VersionedReset, set only by applyVersionedSection; recordedViolations and the daemon-start escalation select on it. Unit rows: TestLoad_WrongTypeVersionedBlockIsNotAReset (new) and TestLoad_NewerSettingsVersionResetsTheWholeBlock (asserts the flag). Real binary: config print on {"runtime":{"migration":5}} creates no state/; doctor config.violations is ok, as on base. Docs (§1 'Both write the same list', line 289, the §6 table) are accurate again without edits.
- **fixed**: round-3 review nit: doctor configViolationsRow counts a leaf twice when it is in both the live load and the persisted record
  - Held. Red on f2870cd9: TestDoctor_ViolationInBothSourcesCountsOnce got '2 setting(s) fell back to the default', with the key listed twice. Fix d64129b2: persisted entries whose key is already live (or already seen) are skipped; the row now reads '1 setting(s)' and shows the live entry with got and want. A reset that only the record holds still shows (TestHookCapture_ResolvedConfigRemovesViolationsRecord green).

### Tests

- `go test -p 1 ./internal/cli -run '^TestLoadConfigAndReport_WrongTypeBlockIsNotAReset$' -count 1 (on f2870cd9 code, with the new row only)`: RED: both subtests, 'a daemon start does not report a block it did not reset: [configuration block reset to defaults]'. With the earlier assertions commented out in turn: '[]string(nil) does not contain "configuration warning"' and 'a command load records no setting for a wrong-type block'
- `go test -p 1 ./internal/cli -run '^TestDoctor_ViolationInBothSourcesCountsOnce$' -count 1 (on f2870cd9 code)`: RED: expected '1 setting(s) fell back to the default', actual '2 setting(s) ...'
- `go test -p 1 ./internal/cli -run '^(TestLoadConfigAndReport_WrongTypeBlockIsNotAReset|TestDoctor_ViolationInBothSourcesCountsOnce|TestLoadConfigAndReport_RecordFollowsTheLoad|TestDaemonStart_ReportsAnUnchangedConfigOnce|TestHookCapture_ResolvedConfigRemovesViolationsRecord|TestHookCapture_NewerSettingsVersionWarnsWithoutLoud)$' -count 20`: ok (29.8s) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 1 ./internal/config -run '^(TestLoad_WrongTypeVersionedBlockIsNotAReset|TestLoad_NewerSettingsVersionResetsTheWholeBlock)$' -count 20`: ok (1.4s) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 1 -race ./internal/cli -run '^(TestLoadConfigAndReport_WrongTypeBlockIsNotAReset|TestDoctor_ViolationInBothSourcesCountsOnce|TestLoadConfigAndReport_RecordFollowsTheLoad|TestDaemonStart_ReportsAnUnchangedConfigOnce|TestHookCapture_ResolvedConfigRemovesViolationsRecord|TestHookCapture_NewerSettingsVersionWarnsWithoutLoud)$' -count 3`: ok (6.0s) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 1 -race ./internal/config -run '^(TestLoad_WrongTypeVersionedBlockIsNotAReset|TestLoad_NewerSettingsVersionResetsTheWholeBlock)$' -count 3`: ok (2.0s) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 1 -timeout=30m ./internal/config -count 1`: ok (18.7s)
- `go test -p 1 -timeout=30m ./internal/cli -count 1`: ok (130.6s), exit 0
- `go test -p 1 ./test/docs ./test/guards -count 1`: ok / ok
- `GOOS=windows|linux|darwin go vet ./internal/cli ./internal/config; go build ./...; go vet ./internal/... ./test/...`: clean
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/cli/... ./internal/config/... (and the pinned binary with GOOS=linux)`: clean / clean
- `go run ./tools/devtool fmt-check`: rc=0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS both
- `go run ./tools/devtool check-commit-msg <ff0977c0 and d64129b2 messages>`: rc=0 both; no attribution trailers
- `real binary: qompack config print in a scratch project with .qompack/config.json {"runtime":{"migration":5}}, HOME/USERPROFILE redirected, daemon disabled; then qompack doctor --json`: no .qompack/state created; config.violations status ok, observed none
- `carried from round 2: Linux binary -test.run '^TestHookCapture_FIFORecordDoesNotBlockTheHook$' -test.count 20`: PASS 20/20 at 07ff04ad; not re-run, because the round-3 changes do not touch recordHolds or the hook write path

### Criterion changes

- Round 1: TestHookCapture_InvalidValueStaysLoud (1 LOUD line) is replaced by TestHookCapture_InvalidValueWarnsWithoutLoud (0 LOUD lines, N warn lines). Reason: C4.9/D59.
- Round 1: TestHookCapture_NewerSettingsVersionWarnsWithoutLoud runs once per config.VersionedSections() entry (adds runtime.phase7).
- Round 1: doctor config.violations Observed text changes from '%d leaf/leaves ...' to '%d setting(s) fell back to the default'. Reason: a block reset is not a leaf.
- Round 2 (tightened): TestDaemonStart_ReportsAnUnchangedConfigOnce requires exactly 1 reset Loud line in LOUD.log and in the Loud ring. Reason: D59, loud once per daemon start.
- Round 2 (tightened): TestLoadConfigAndReport_RecordFollowsTheLoad requires both writers to produce identical bytes with no rewrite.
- Round 2 (new rows): TestHookCapture_FIFORecordDoesNotBlockTheHook (unix) and TestRecordHolds_OnlyAPlainFileWithTheSameBytes.
- Round 3 (new row): TestLoadConfigAndReport_WrongTypeBlockIsNotAReset. A versioned block that is not an object is never recorded by a daemon-start, command or hook load, and is never Louded as a reset. Reason: round-3 review finding.
- Round 3 (new row and tightened row): TestLoad_WrongTypeVersionedBlockIsNotAReset is new. TestLoad_NewerSettingsVersionResetsTheWholeBlock now also asserts VersionedReset true on the reset and false on the unknown key. Reason: pins the field the cli selects on.
- Round 3 (tightened doctor wording): doctor counts a setting present in both the live load and the record once and shows the live entry (TestDoctor_ViolationInBothSourcesCountsOnce). Reason: round-3 review nit; it was double-counted before.

### Open issues

- Carried: the /qompack: commands and `qompack mcp` call LoadConfigAndReport with a real logger, so each run Louds every §11.3 leaf violation into LOUD.log. A reset is logged at warn on those paths. This predates this work and is documented in the §6 table.
- Carried: docs/config-reference.md:207-208 (generated) says a refused gated switch 'is reported as a warning', but the code records it as an invalid value. The generator input is not mine.
- Carried, scope grown: CHANGELOG/release notes do not mention the logging changes (hooks warn; no re-Loud of an unchanged file at daemon start; a reset is Loud once per daemon start), the removal of config-violations.json, that commands record resets, or that doctor now counts a setting once. CHANGELOG is not my file.
- Carried: the historical Result block of docs/uat.md UAT-11 lists LOUD.log for the settingsVersion step. It records a past run and was not edited.
- Carried, predates this work: doctorPersistedViolations reads the record with an unbounded paths.ReadFileShared, so a FIFO there would hang doctor (not hooks; the next hook replaces such a FIFO with a plain file). doctor's other state reads have the same shape, so a one-off fix would be piecemeal. The double count is closed in d64129b2.
- Carried: a command load whose newer-settingsVersion reset comes from the user-global, env or --set layer, in a project with no .qompack, creates .qompack/state/config-violations.json. Base already did this for a leaf violation.
- Carried, unverified: the unix-only FIFO row ran without -race on Linux. This Windows host has no Linux C toolchain to build a -race binary; hosted CI's Linux -race lane will cover it.
- Closed in round 3: the wrong-type-block regression (ff0977c0) and doctor's double count (d64129b2).

### Needs owner

- Decision under D33, carried from round 2: a newer-settingsVersion block reset (now identified by config.Warning.VersionedReset) is Loud once per daemon start, so status shows it after a restart on an unchanged downgraded file. An unknown key, a wrong type (including a versioned block that is not an object) or a retired meaning stays warn at start. Every class is Loud on a reload of a changed file or on admin.reload. Hooks, commands and mcp log a reset at warn.
- New exported field config.Warning.VersionedReset (bool, no new number). It follows the Deprecated precedent. Consumers that record or escalate a reset must select on it, not on Key.
- Test-only constant fifoHangWatchdog = 30s (internal/cli/capture_config_fifo_unix_test.go): a hang detector, not a performance bound. Only a regression reaches it, and it matches the store watchdog convention.
- Carried: test-only pinnedRecordTime = 2001-02-03T04:05:06Z, an mtime no write during a test can produce, used to detect a replaced record without a clock margin. No new numeric constants in product code.

## verify:config: verdict `sound`, 0 finding(s)



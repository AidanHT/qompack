# Config workstream report — C1.8 capture config fallback

Branch `closeout/config`. Workflow `wf_16dd5d95-b3a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `1062c506404b967316c8aed3e9becaf538bf0386`

### Root cause

C1.8 was part-fixed on cf31e01 already. Finding S-7 (ada54d1, 2026-09-16) had fixed three of SP-18's rows: an invalid value, a newer settingsVersion and a refused gated switch. I reproduced the rest with the real binary (runs/repro-before-cf31e01-windows.log). With an unknown key, a wrong-type leaf, or both beside an invalid value, the observe-prompt hook spooled nothing and .qompack/ held only config.json. In every case self-test said config.load ok.

Mechanism: config.LoadForCapture (internal/config/capture_load.go) still ran `if len(warnings) != 0 { return fail() }` after each file merge and after the environment and --set layers. So every Warning refused the whole delivery. That covers an unknown key, a wrong type, a section that is not an object, an unparseable QOMPACK_* or --set value, and an unknown --set key. admitHookCapture then returned an error and doHook printed {} with nothing recorded. config.Load treats the same Warnings as drops of one leaf each.

self-test's only config check (config.load, selfTestConfigLoad) and doctor both load through config.Load, which never refuses. Neither ever asked the hook path's loader.

Found while fixing: config.Load itself silently threw away every layer over one bad leaf. parseLeafString accepted NaN/Inf, and coerceLeafValue accepted any whole number for an int leaf (e.g. 1e300). fromMap then failed to decode and fell back to Defaults() with no warning (red: runs/red-load-unrepresentable-windows.log).

Also caught in my own new code before finishing: `doctor --project .` passed the relative root through, and LoadForCapture refuses relative roots, so a clean project read "refused" (red: runs/red-doctor-relative-project-windows.log; fixed in 9850a6c).

### Summary

C1.8 is fixed on closeout/config (cf31e01..1062c50, 7 commits, nothing pushed). One unknown or mistyped config key no longer stops the hooks recording, and self-test/doctor now say when the hooks refuse or only partly apply a config. The harness refused to let me write plans/sdd/V6-closeout/config/report.md, so the report is in this output (below plus the other fields) for the coordinator to commit; I did not get around the refusal. The run evidence is committed under plans/sdd/V6-closeout/config/runs/.

WHAT CHANGED
- LoadForCapture now follows config.Load's per-leaf rules and returns `(Config, Provenance, []Violation, []Warning, error)`.
  - An unknown key, wrong type, non-object section, or unparseable/nonfinite QOMPACK_* or --set value is dropped and returned as a Warning. The leaf keeps the value from the layer below.
  - Invalid values still clamp, and a newer settingsVersion still resets its block (both from S-7).
  - Its result equals Load's for every per-leaf case; TestLoadForCapture_AgreesWithLoadOnEveryPerLeafProblem checks the same Config, the same violations, and that every capture warning is also a Load warning.
- Hooks log each warning at warn level (reportCaptureConfig). Violations are still Loud and still persisted to config-violations.json; unknown keys never go there.
- backup still refuses on any violation or warning, which is effectively what it did before.
- A refusal now names its class in a fixed phrase and never echoes the input.
- config.Load's type checks reject NaN/Inf and integers past the platform int. One bad leaf no longer silently resets every layer.
- self-test has a new check, config.capture, right after config.load. It runs LoadForCapture read-only:
  - refused → critical, exit 1;
  - applied with fallbacks or dropped keys → warn, naming every key;
  - clean → ok.
- doctor's recording section has a matching config.capture row. It appears even when .qompack is missing or holds only config.json, which is the state a refusal leaves.

WHAT STILL REFUSES CAPTURE (structural), and why
- Unchanged security bounds:
  - a relative root;
  - a config file that is not a plain regular file under 1 MiB, or is swapped between check and open;
  - an env/--set value over 64 KiB each or 1 MiB total, or not UTF-8.
- A config file that is not one strict JSONC object. A file that does not parse cannot be applied leaf by leaf, because nobody can tell which of its leaves were privacy rules.
- Any problem inside runtime.redact, or any fallback that would change that block. Falling back there would record under a weaker privacy policy than the one written. For example, a mistyped `enabled` in the project file would leave the user file's `false` in force. This matches redact's existing refusal of a pattern that does not compile. These inputs already refused on cf31e01; what is new is that this is the only per-leaf problem that still refuses. A malformed parent such as `"runtime": 5` does not refuse, since it carries no policy to lose.
- Backstops: the effective config does not decode, is still invalid after fallback, or exceeds the pattern-rule bounds.

AFTER-FIX PROBE (runs/repro-after-8b7a605-windows.log)
Of 10 one-file projects, all 8 per-leaf cases recorded: two read ok and six read warn with the keys named. The mistyped redact pattern list and the unparseable file recorded nothing, and self-test failed critically (exit 1) naming the class. config.load read ok in all ten. The unknown-key warning reached the day log.

DOCS (82144cc)
- troubleshooting.md: §6 rewritten; §1 explains config.capture beside config.load and fixes two stale statements about the violations file and the day log; §10 bullet updated.
- uat.md: the two scenarios that used an unknown key to force a capture failure now use an unparseable file; the self-check list is eight rows.
- README, security.md and CHANGELOG state the two exceptions.
- config-reference.md: the header text lives in tools/devtool/genconfigdocs.go; I changed it and regenerated with gen-config-docs.

TEST CHANGES
Two criterion changes, detailed in criterion_changes. The only other edits to existing tests add stricter warning assertions.

DECISIONS
- recovery-review.md asked to confirm the loader always gives runBackup a real SettingsVersion. It does: any value other than 1 is a violation (validate.go:351) and a mistyped one is a warning, and backup refuses on either.
- The runtime.redact all-or-nothing rule is the one new contract; it is Q1 in needs_owner.

Whether to run test/e2e, test/security and test/fault is the coordinator's call (details in open_issues).

### Commits

- 61b308a fix(config): stop one unrepresentable leaf resetting all layers
- 3d3c5bd fix(config): fall back per leaf in the capture loader
- 20fce35 fix(cli): report capture-config refusal in self-test and doctor
- 8b7a605 fix(cli): count a settingsVersion reset as a setting, not a value
- 82144cc docs(config): describe the hook path's per-leaf config contract
- 9850a6c fix(cli): resolve doctor's project root before the capture check
- 1062c50 docs(config): commit C1.8 run evidence

### Tests

- `go test ./internal/config -run 'TestLoadForCapture_PerLeafProblemsFallBackInsteadOfRefusing|TestLoadForCapture_AgreesWithLoadOnEveryPerLeafProblem' -count=1 (unmodified code, Windows)` — FAIL as intended (red): all 12 cases in both tests got 'capture configuration unavailable' — runs/red-config-unit-windows.log
- `go test ./internal/config -run TestLoad_UnrepresentableLeafWarnsWithoutResettingEverything -count=1 (unmodified code, Windows)` — FAIL as intended (red): all 3 cases got budgetTokens 12000 instead of 9000 — runs/red-load-unrepresentable-windows.log
- `go test ./internal/cli -run 'TestHookCapture_UnknownKeyAndInvalidLeafStillCapture|TestHookCapture_MistypedLeafStillCaptures|TestSelfTest_ReportsACaptureConfig|TestSelfTest_CleanCaptureConfigIsOK|TestDoctor_ReportsTheCaptureConfiguration' -count=1 -v (unmodified code, Windows)` — FAIL as intended (red), 6 tests: hooks created no spool; self-test and doctor had no config.capture — runs/red-cli-hook-selftest-doctor-windows.log
- `go test ./internal/cli -run TestDoctor_CaptureConfigResolvesARelativeProject -count=1 -v (before 9850a6c, Windows)` — FAIL as intended (red): 'project or home root is not an absolute path' — runs/red-doctor-relative-project-windows.log
- `go test ./internal/config -run 'LoadForCapture|TestLoad_Unrepresentable' -count=1 -v (Windows)` — PASS, 16 top-level tests — runs/green-config-focused-windows.log
- `go test ./internal/cli -run 'TestHookCapture_|TestSelfTest_|TestDoctor_|TestBackup|TestHooks_' -count=1 -v (Windows)` — PASS, 53 top-level tests — runs/green-cli-focused-windows.log. The first attempt (-1.log) failed only on the criterion-changed config-corrupt self-test, as predicted
- `go test ./internal/cli ./internal/config -count=1 -timeout=30m (full packages at 9850a6c, Windows)` — PASS (cli 53.6s, config 7.6s) — runs/full-cli-config-windows-9850a6c.log; earlier full runs also PASS (full-internal-*-windows.log)
- `git checkout-index of the staged 3d3c5bd tree, then go build ./... && go vet && go test ./internal/config ./internal/cli (bisectability check)` — PASS (not logged)
- `go test ./test/docs -count=1 (Windows)` — PASS — runs/test-docs-windows.log
- `go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check` — all exit 0
- `go run ./tools/devtool fmt-check; go vet ./internal/config ./internal/cli ./tools/devtool` — clean
- `go run ./tools/devtool lint (at 82144cc)` — Stopped by me at stubskips, which runs a whole-tree go test the brief forbids. I stopped only my own process tree (26 PIDs, recorded first; no orphans). Everything before it passed except bindeps (pre-existing) — runs/lint-windows.log
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,runpatterns,docmarkers,coveragefloors (at 9850a6c)` — All PASS except bindeps: 4 violations (golang.org/x/sys/unix on linux/darwin), identical on base cf31e01 — runs/lint-only-windows-9850a6c.log, runs/lint-bindeps-base-cf31e01-windows.log
- `Linux container, non-root qompack-test uid 10001, -race, GOMAXPROCS=4, fresh /work/cx-config-82144cc-20260922T215444Z from a git bundle: go test ./internal/config ./internal/cli ./test/docs ./tools/devtool` — All PASS (config 284, cli 317, docs 19, devtool 250), no race reports — runs/linux-cx-config-82144cc-20260922T215444Z.log. The first attempt never ran because Git Bash rewrote /work; the rerun used MSYS_NO_PATHCONV=1
- `Linux container, same setup, /work/cx-config-9850a6c-20260922T215950Z (final code): go test ./internal/config ./internal/cli ./test/docs` — All PASS (config 284, cli 318, docs 19), no race reports — runs/linux-cx-config-9850a6c-20260922T215950Z.log
- `runs/probe.sh with the real qompack binary: observe-prompt hook + config print + self-test --json over 9-10 one-file project configs, before (cf31e01) and after (8b7a605)` — Before: an unknown key, a mistyped leaf, or both beside an invalid value spooled nothing, and self-test said ok everywhere. After: every per-leaf case records; the mistyped redact pattern list and the unparseable file refuse with a critical config.capture (exit 1). Every daemon these runs spawned was stopped by the PID in its own lock file; none left over

### Criterion changes

- TestLoadForCapture_RefusesSchemaEnvFlagAndEffectiveConfigurationFailures -> TestLoadForCapture_RefusesAnyPrivacyPolicyProblem (3d3c5bd). Old: refuse the whole capture for an unknown key, a wrong leaf type, a NaN env value, an unknown flag or a NaN flag value anywhere in the schema. New: those five rows, with the same inputs, are in captureFallbackCases and must fall back per leaf and agree with config.Load. The refusal check, including 'reveals no input', is kept for seven runtime.redact shapes, where it is a real security bound. Why: the old test asserted the C1.8 defect itself, the opposite of the documented contract (README 'Configuration', docs/config-reference.md).
- TestSelfTest_ConfigCorruptFallsBackAndExitsZero -> TestSelfTest_ConfigCorruptFallsBackButReportsTheRefusedCapture (20fce35). Old: exit 0 under the config-corrupt fault. New: exit 1, config.capture critical and naming the 'not a single strict JSONC object' class, config.load still ok, no other critical row; the check that the fault really fired is kept. Why: the fault truncates config.json, which the hook path refuses outright (TestHookCapture_FaultsCannotBypassAdmission pins this), so every hook records nothing and exit 0 is the dishonest 'ok' C1.8 removes. The test's own stated invariant — config.load is never critical — still holds.

### Open issues

- plans/sdd/V6-closeout/config/report.md was NOT written: the harness blocks this agent from writing report .md files, and I did not work around it. The report is in this output (summary, root_cause, tests, criterion_changes and these fields); the coordinator can commit it at that path. The evidence commit 1062c50 says the same.
- devtool lint's bindeps check fails on the base cf31e01 as well, not from this work: golang.org/x/sys/unix reaches the shipped linux/darwin binaries through internal/paths/rename_noreplace_{linux,darwin}.go (runs/lint-bindeps-base-cf31e01-windows.log). This belongs to whoever owns the restore no-replace rename (C1.7 lineage).
- devtool lint's stubskips check runs a whole-tree `go test`, so the 'run devtool lint once' instruction conflicts with 'never run the whole tree'. I stopped it; the coordinator's integrated gate should cover stubskips. This change adds no t.Skip.
- Not run by design: the whole tree, test/e2e, test/security, test/fault. Comments in test/e2e and test/security still describe LoadForCapture as refusing on any warning; nothing there calls it, so nothing breaks, but the comments are stale.
- A stale state/config-violations.json is never cleared after the config is repaired (neither loader writes when there are no violations), so doctor's config.violations row keeps showing fixed keys. Pre-existing. The new config.capture row reads the live config and is not affected.
- Linux: my run directories /work/cx-config-82144cc-20260922T215444Z and /work/cx-config-9850a6c-20260922T215950Z (with -artifacts, .bundle and .inner.sh) are left in place as evidence. The .inner.sh is a copy of the close-out's linux-nonroot-inner.sh.

### Needs the owner

- Q1: Confirm that on the hook path any problem inside runtime.redact refuses capture, rather than falling back leaf by leaf and recording under a weaker privacy policy than the one written. This is my default, the code already refused these inputs on cf31e01, and troubleshooting §6, README, security.md and the generated reference header now document it.
- Q2: A wrong-type value is a Warning, not a recorded violation, in BOTH loaders, so it never reaches config-violations.json. README's wording ('an invalid value ... is recorded') suggests it would. I documented the behaviour as it is (a fifth class in troubleshooting §6) and did not change ViolationsFromWarnings or Load. Recording it would be a contract change for the owner.
- Q3 (pre-existing, unchanged): self-test spawns the project daemon through daemon.EnsureRunning even when config.capture says every hook would refuse.

## Independent review

### review:config:both: needs-fixes

- **major** `internal/config/load.go:519-524 (parseLeafString, case kindInt)` — Commit 61b308a says one unrepresentable integer can no longer wipe every config layer. That is only true for config files, because only coerceLeafValue got the bound. A QOMPACK_* or --set integer still goes through strconv.ParseInt(raw,10,64) and then float64(n). Any value from 2^63-512 up to MaxInt64 (for example 9223372036854775807) rounds up to 2^63, which will not decode into a Go int. Load then answers with fromMap's Defaults(): every layer is thrown away and no warning is given. LoadForCapture refuses the whole capture with refuseDecode over a leaf that has nothing to do with redaction, which breaks the per-leaf contract this task was meant to restore. The CHANGELOG line 'One nonfinite QOMPACK_*/--set float or out-of-range integer no longer resets every configuration layer' is false for these values. The same path also skips the int-size check on 32-bit targets.
  - Evidence: Scratch copy of HEAD 1062c50, project file {"checkpoint":{"budgetTokens":9000}}. With QOMPACK_RUNTIME__DAEMON__MAXSESSIONS=9223372036854775807, Load returned budget=12000 (the default) and warns=[]. LoadForCapture returned err='capture configuration unavailable: the effective configuration does not decode', and --set runtime.daemon.maxSessions=9223372036854775807 gave the same refusal. The control value 9223372036854775000 gave budget=9000 with no error. TestLoad_UnrepresentableLeafWarnsWithoutResettingEverything only has an integer-overflow row for the file layer (1e300).
  - Fix: In parseLeafString's kindInt case, parse with strconv.ParseInt(raw, 10, strconv.IntSize). Convert with f := float64(n) and reject f < float64(math.MinInt) || f >= -float64(math.MinInt), the same guard coerceLeafValue uses, so the value becomes an ordinary 'invalid env value' or 'invalid --set value' warning. Add env and --set rows with 9223372036854775807 to TestLoad_UnrepresentableLeafWarnsWithoutResettingEverything, and a matching row to captureFallbackCases / TestLoadForCapture_AgreesWithLoadOnEveryPerLeafProblem.
- **minor** `internal/config/capture_load.go:190-196 (policy re-check) and inCapturePolicy` — The new structural rule protects only runtime.redact, on the grounds that a fallback must never capture under a weaker privacy policy than the one written. runtime.mode='off' is the other operator control for whether capture happens (capture_admission.go:121 returns before admitting). The same argument applies to it, and C1.8 makes it worse. Before this change, a mistyped mode in a file ("mode": false or 0) refused capture, so it behaved like off. Now the mistype is dropped with a warning, the lower layer or default ('auto') stays in force, and the hook records. An invalid string such as "OFF" has clamped to 'auto' and recorded since S-7. The privacy boundary is drawn around one of two opt-out controls, and needs_owner Q1 does not mention it.
  - Evidence: defaults.go:113 Mode: "auto"; runtime.go:14 enum auto|full|passive|off. inCapturePolicy only matches the 'runtime.redact' prefix. The post-fallback check compares only Redact.Enabled and Redact.Patterns.
  - Fix: Either treat any layer's attempt to set runtime.mode that fails (a warning or a clamp keyed runtime.mode) as refusePolicy on the hook path, or state explicitly in needs_owner Q1 and troubleshooting §6 that runtime.mode is deliberately outside the fail-closed set. Then add a test pinning whichever is chosen.
- **nit** `plans/V2-SP-05-daemon-ipc-and-hot-path.md:1631 and plans/sdd/V2-SP-05-daemon-ipc-and-hot-path/task-6-spec.md:90` — The criterion change in TestSelfTest_ConfigCorrupt… (self-test now exits 1 under config-corrupt) contradicts the fault-table row that still says self-test gives 'per-leaf fallback + Loud (§11.3); exit 0'. The change itself is justified, since hooks do refuse that file. The plan row, though, is now wrong on both the hook half and the self-test half, and no plan, inventory or TRACEABILITY note records the new expectation.
  - Evidence: The row reads: 'the hot path never parses config, so this is inert for hooks and exercised by `qompack daemon`/`self-test`: per-leaf fallback + `Loud` (§11.3); exit 0'. No commit in cf31e01..1062c50 touches plans/V2-SP-05*.
  - Fix: Record the superseded expectation where V6 close-out tracks criterion changes, either the report the coordinator commits or an inventory row, citing 20fce35. A dated erratum on the plan row would also do.
- **nit** `docs/troubleshooting.md:354 and :390-395` — Two small inaccuracies in §6. First, the 'newer settingsVersion' row says it appears as 'warn in the day log'. On the hook path that reset is returned as a Violation, and reportCaptureConfig logs it with Loud, not Warn. Second, the refusal table lists four DETAIL classes but leaves out the reachable 'runtime.redact.patterns exceeds the capture rule bounds' (more than 256 patterns, or one over 64 KiB). It also leaves out the decode and still-invalid backstops, so an operator who sees that DETAIL text will find no entry for it.
  - Evidence: capture_load.go returns versionWarns as Violations. config.go reportCaptureConfig calls log.Loud for every violation. The refuseRules text does not contain 'a runtime.redact setting cannot be applied'.
  - Fix: Change the row to say 'warn (config print/daemon) or loud (hooks) in the day log', and add a row for the rule-bounds class (plus one line on the backstops) to the refusal table.
- **nit** `plans/sdd/V6-closeout/config/runs/red-cli-hook-selftest-doctor-windows.log, red-load-unrepresentable-windows.log` — The committed red evidence comes from an earlier layout of the tests. The self-test and doctor failures point to capture_config_test.go:150/165/177/210, but those tests now live in capture_config_report_test.go. The Load failure points to capture_load_fallback_test.go:208, but TestLoad_UnrepresentableLeafWarnsWithoutResettingEverything now lives in load_test.go. So the logs do not show that the committed test bodies fail on the unfixed code. I read the tests, and the assertions would plausibly still fail on cf31e01.
  - Evidence: From the red log: 'capture_config_test.go:150: self-test reported no "config.capture" check' and 'capture_load_fallback_test.go:208 ... expected: 9000 actual: 12000'.
  - Fix: Re-run the red commands with the committed test files on top of cf31e01's code, in a scratch copy, and replace or add to the two logs. Otherwise, note in the report that the tests were moved after the red run.

## Fix seat (review resolution) — status `partial`, head `2d34a7f`

### Root cause

On cf31e01, config.LoadForCapture turned every merge Warning into a refusal of the whole delivery (capture_load.go lines 102-115). Only S-7's out-of-range-value clamp had been fixed. So one unknown key, wrong-type leaf, non-object section or unparseable QOMPACK_*/--set value made every hook admit nothing, while the hook printed {} and exited 0 and self-test asked only the soft loader and said config.load ok. Separately, config.Load's merge-time type gates let through values no int/float field can hold: NaN or Inf from env/--set, whole numbers past MaxInt in a file (fixed by 61b308a), and, per review finding 1, env/--set integers from 2^63-512 up, which round to 2^63 as a float64. fromMap then silently reset every layer to Defaults().

### Summary

Both review findings were checked independently, confirmed, and fixed test-first. All code, tests, docs and evidence are committed on closeout/config (head 2d34a7f, clean tree, nothing pushed). Status is partial for one reason: the required committed report plans/sdd/V6-closeout/config/report.md does not exist. The harness refused the write ("Subagents should return findings as text, not write report files"), the same refusal the implementer hit (1062c50). I did not get around it through the shell. The report content is below, and the coordinator must commit it if it wants the file on the branch. Evidence paths are relative to plans/sdd/V6-closeout/config/runs/.

REPORT: C1.8, the hook path's config loader.

1. Root cause (implementer's work, confirmed from its commits and evidence). On cf31e01, LoadForCapture turned EVERY merge Warning into a refusal: `if len(warnings)!=0 {return fail()}` after each file layer and after the env and --set layers (git show cf31e01:internal/config/capture_load.go, lines 102-115). S-7 (ada54d1) had fixed only out-of-range values. So one unknown key, wrong-type leaf, non-object section, or unparseable QOMPACK_*/--set value made every hook admit nothing. The hook still printed {} and exited 0, and self-test said config.load ok. Real-binary proof is in repro-before-cf31e01-windows.log: for {"runtime":{"notAKey":1}}, {"checkpoint":{"budgetTokens":"12000"}} and {"runtime":{"mode":"sideways","notAKey":1}}, zero spool files were written and .qompack/ held only config.json. A second defect: config.Load's type gates let through NaN/Inf from env/--set and 1e300 in a file. fromMap then fell back to Defaults(), silently discarding every layer.

2. What changed. 
- 61b308a: nonfinite floats and file integers outside int range become per-leaf warnings. 
- 3d3c5bd: LoadForCapture follows Load's per-leaf contract and returns []Warning. The hook reports them at Warn. backup still refuses on any warning.
- 20fce35: self-test and doctor gain a config.capture row: critical with exit 1 on a refusal, warn naming every key on a fallback, ok when clean. 
- 8b7a605: a block reset is counted as a "setting". 
- 82144cc: docs. 
- 9850a6c: doctor --project . resolves the absolute root before the capture check. 
- a8ead17 and f846f91: the review fixes (section 7). 
- ca54596: docs for them.

What still refuses capture on the hook path (structural): a relative root; a config file that is not a bounded regular file; a file that is not one strict JSONC object; an env/--set value over the bounds or not UTF-8; any runtime.redact setting that cannot be applied as written; any runtime.mode setting that cannot be applied as written; and the decode/invalid/rule-bound backstops. Everything else falls back per leaf. Whenever LoadForCapture does not refuse, its result equals Load's (TestLoadForCapture_AgreesWithLoadOnEveryPerLeafProblem).

3. Recorded decisions changed. S-7 said "LoadForCapture clamps and returns the violations". f846f91 narrows that for runtime.mode on the hook path only: an invalid mode now refuses instead of clamping. S-7's justification ("bounded again downstream") does not cover an operator switch. The S-7 posture test uses runtime.telemetry.enabled and is unaffected. 3d3c5bd's redact-only boundary is extended to runtime.mode. No document under plans/sdd/V6-remediation/ defines this contract.

4. Tests (Windows, co-loaded shared machine; no timing assertion failed, so no co-load reruns were needed).
- Implementer: red logs (red-config-unit, red-cli-hook-selftest-doctor, red-load-unrepresentable, red-doctor-relative-project) and green logs (green-*, full-*, test-docs), all ok.
- Implementer, Linux (non-root uid 10001, -race): at 9850a6c, config 284, cli 318, test/docs 19, all pass. At 82144cc, devtool 250 also passes.
- Fix seat: the exact commands are in the tests field. Real-binary probes: repro-after-f846f91-windows.log (12 projects) and repro-int64-env-before-after-windows.log.
- Fix seat, Linux at ca54596 (fresh /work/cx-config-review-ca54596-20260922T224447Z, bundle sha256-checked, uid 10001, -race, GOMAXPROCS=4): internal/config 311, internal/cli 323, test/docs 19, tools/devtool 250 passed, 0 failed. No race log, source tree unchanged.
- Lint (every sub-check except stubskips): all pass except bindeps. Its four golang.org/x/sys/unix violations are byte-identical to base cf31e01, and go.mod/go.sum are unchanged.

5. Criterion changes: see the criterion_changes field.

6. Open items and owner decisions: see the open_issues and needs_owner fields.

7. Review resolution.

Finding 1 (major), CONFIRMED and FIXED in a8ead17. parseLeafString parsed ints with ParseInt(raw,10,64) and returned float64(n). Any value from 2^63-512 up to MaxInt64 rounds to 2^63, which no Go int decodes. Load then silently fell back to Defaults() for every layer, and LoadForCapture refused with "does not decode". Only the file layer had been bounded. The 32-bit half is latent: all six release targets are 64-bit.
- Red: review-red-int64-windows.log. Load returned budget 12000 instead of the file's 9000, and LoadForCapture refused, exactly as the reviewer said. The real binary at 1062c50 did the same: 0 spool files, config print showing 12000 with no warning, self-test critical.
- Fix: parse at strconv.IntSize and apply coerceLeafValue's bound (f < MinInt or f >= -MinInt).
- Tests: MaxInt64 env and --set rows added to TestLoad_UnrepresentableLeafWarnsWithoutResettingEverything and to captureFallbackCases (and so to both LoadForCapture tests), plus a file-literal guard row.
- Green on windows/amd64, windows/386 and Linux -race. The real binary at f846f91 records, keeps budget 9000, and warns naming the key. The CHANGELOG line is now true and was reworded.

Finding 2 (minor), CONFIRMED and FIXED in f846f91 by failing closed.
- Confirmed: inCapturePolicy covered only runtime.redact. At cf31e01, a mistyped mode (false) refused, but after 3d3c5bd it recorded. An out-of-enum mode ("OFF") had clamped to auto and recorded since S-7 (repro-before p4). 
- Worse case the review missed: the clamp restores the DEFAULT, so a project's "OFF" over a user's valid "off" also recorded. 
- runtime.mode=off is a documented safe-disable rung. So I took the review's first option: a new refuseSwitch class fires when any layer's runtime.mode (or a key under it) warns, or when the fallback would change the mode. 
- A non-object runtime ancestor still only warns, valid values apply as before, and config.Load still clamps.
- Red first: review-red-mode-config/cli-windows.log. Pinned by TestLoadForCapture_RefusesAnyCaptureSwitchProblem (9 rows: refuses, never echoes the value, Load still succeeds), the boundary tests AppliesEveryValidCaptureSwitchValue and CaptureSwitchAncestorProblemDoesNotRefuse, TestHookCapture_UnappliableCaptureSwitchRecordsNothing (real hook: no spool, class logged in hook-quiet, value not echoed), TestSelfTest_ReportsACaptureSwitchRefusal, two new RefusesBeforeSpoolAndDaemonStart rows, and a doctor "refused switch" row.
- Docs: troubleshooting §1, §6 and safe-disable step 3, README, security.md, install.md, CHANGELOG, and the regenerated config-reference header. The §6 observed table was re-measured with the real binary.
- This choice is flagged for the owner in needs_owner Q1.

Process: I had no tool to spawn subagents, so I ran work concurrently instead: the Linux run, Windows lint, and the full-package and probe runs in the background. The only processes I stopped were daemons my own probes spawned, each checked by command line. One of them wrote its lock after the probe had checked for it and was stopped separately; the probe log notes this.

### Commits

- a8ead17 fix(config): bound env and --set integers to the platform int
- f846f91 fix(config): refuse capture when runtime.mode cannot apply
- ca54596 docs(config): state that runtime.mode fails closed on the hook path
- 2d34a7f docs(config): commit C1.8 review-resolution run evidence
- (implementer, unchanged) 61b308a, 3d3c5bd, 20fce35, 8b7a605, 82144cc, 9850a6c, 1062c50

### Tests

- `go test ./internal/config -run 'TestLoad_UnrepresentableLeafWarnsWithoutResettingEverything|TestLoadForCapture_PerLeafProblemsFallBackInsteadOfRefusing|TestLoadForCapture_AgreesWithLoadOnEveryPerLeafProblem' -count=1 -timeout=30m -v (1062c50 + new rows)` — RED as intended: 6 int64 env/--set subtests FAIL (budget 12000 vs 9000; 'does not decode'), file-literal guard PASS; review-red-int64-windows.log
- `go test ./internal/config -run '...same three...|TestLoad_Env|TestLoad_Flags' -count=1 -timeout=30m -v (after a8ead17 fix)` — ok; review-green-int64-windows.log
- `GOARCH=386 CGO_ENABLED=0 go test ./internal/config -run '...same three...' -count=1 -timeout=30m -v` — ok; review-green-int64-windows-386.log
- `go test ./internal/config -run 'TestLoadForCapture_RefusesAnyCaptureSwitchProblem|TestLoadForCapture_AppliesEveryValidCaptureSwitchValue|TestLoadForCapture_CaptureSwitchAncestorProblemDoesNotRefuse' -count=1 -timeout=30m -v (before f846f91 fix)` — RED as intended: 9/9 switch rows FAIL, boundary pins PASS; review-red-mode-config-windows.log
- `go test ./internal/cli -run 'TestHookCapture_RefusesBeforeSpoolAndDaemonStart|TestHookCapture_UnappliableCaptureSwitchRecordsNothing|TestSelfTest_ReportsACaptureSwitchRefusal|TestDoctor_ReportsTheCaptureConfiguration' -count=1 -timeout=30m -v (before fix)` — RED as intended: new mode rows/tests FAIL (hook spooled an operator's "OFF"); review-red-mode-cli-windows.log
- `same config+cli focused sets with fix applied and old runtime.mode "sideways" inputs` — exactly the 5 sideways examples FAIL as predicted (criterion change); review-mode-fix-predicted-{config,cli}-windows.log
- `go test ./internal/config -run 'LoadForCapture|TestLoad_Unrepresentable|TestLoadAndLoadForCapture' -count=1 -timeout=30m -v` — ok (113 PASS lines); review-green-mode-config-windows.log
- `go test ./internal/cli -run 'TestHookCapture|TestSelfTest|TestDoctor_ReportsTheCaptureConfiguration|TestDoctor_CaptureConfig|Backup' -count=1 -timeout=30m -v` — ok (69 PASS lines); review-green-mode-cli-windows.log
- `go test ./internal/config ./internal/cli -count=1 -timeout=30m (full, at f846f91, Windows)` — ok, ok; review-full-cli-config-windows-f846f91.log
- `go vet ./tools/devtool && go test ./tools/devtool -count=1 -timeout=30m (Windows)` — ok; review-full-devtool-windows.log
- `go test ./test/docs -count=1 -timeout=30m (Windows)` — ok; review-test-docs-windows.log
- `go run ./tools/devtool gen-config-docs --check; gen-mcp-docs --check; gen-command-docs --check` — all up to date; review-gen-docs-check-windows.log
- `go run ./tools/devtool fmt-check; go vet ./internal/config ./internal/cli` — exit 0; exit 0
- `Linux container, fresh /work/cx-config-review-ca54596-20260922T224447Z, uid 10001, -race, GOMAXPROCS=4: go test ./internal/config ./internal/cli ./test/docs ./tools/devtool at ca54596` — PASS config 311, cli 323, test/docs 19, devtool 250; 0 fail/skip; no race log; source unchanged; linux-cx-config-review-ca54596-20260922T224447Z.log
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,runpatterns,docmarkers,coveragefloors` — all PASS except bindeps (4 golang.org/x/sys/unix violations, identical to base cf31e01); review-lint-only-windows-ca54596.log
- `real binary: bash runs/probe.sh <f846f91 exe> <scratch> < runs/probe-cases-review.txt` — 12/12 hooks {} exit 0, config.load ok x12; the four runtime.mode projects record nothing with config.capture critical; other rows unchanged; repro-after-f846f91-windows.log
- `real binary: bash runs/probe-int64-env.sh against 1062c50 and f846f91 binaries` — 1062c50: 0 spool, config print budget 12000 silently, self-test critical 'does not decode'; f846f91: 1 spool, budget 9000, self-test warn naming the key; repro-int64-env-before-after-windows.log

### Criterion changes

- (implementer, 3d3c5bd) TestLoadForCapture_RefusesSchemaEnvFlagAndEffectiveConfigurationFailures -> TestLoadForCapture_RefusesAnyPrivacyPolicyProblem. Old: five per-leaf rows (unknown key, wrong type, unparseable env/--set) had to refuse the whole capture. New: the same inputs must fall back (captureFallbackCases), and refusal is kept only for privacy bounds. Why: the old criterion was the defect itself.
- (implementer, 20fce35) TestSelfTest_ConfigCorruptFallsBackAndExitsZero -> TestSelfTest_ConfigCorruptFallsBackButReportsTheRefusedCapture. Old: exit 0 under the config-corrupt fault. New: exit 1, config.capture critical naming the class, config.load still ok, no other critical row. Why: a truncated config.json is a structural refusal on the hook path, so exit 0 was the dishonest ok the task says to remove. The predicted failure is in green-cli-focused-windows-1.log.
- (fix seat, f846f91) Five tests used runtime.mode "sideways" as their example of an invalid value that falls back on the hook path: captureFallbackCases 'invalid value beside an unknown key', TestLoadForCapture_ReturnsWhatItDidNotApply, TestHookCapture_UnknownKeyAndInvalidLeafStillCapture, TestSelfTest_ReportsACaptureConfigDegradation, and the doctor 'degraded' row. Their assertions are unchanged; the invalid leaf is now retrieval.defaultSpan "sideways". Why: review finding 2 makes an invalid runtime.mode a refusal, now pinned by its own tests. They failed exactly as predicted: review-mode-fix-predicted-*-windows.log.

### Open issues

- report.md was NOT written: the harness blocks subagents from writing report files. The full report is in this output's summary for the coordinator to commit at plans/sdd/V6-closeout/config/report.md if it wants the file on the branch.
- The lint sub-check stubskips was not run, because it is a whole-tree `go test ./internal/... ./cmd/... ./test/...` that the brief forbids. It is left to the coordinator's integrated gates. No t.Skip was added on this branch.
- bindeps lint fails with 4 violations (golang.org/x/sys/unix reaches the shipped linux/darwin binary). These are identical to base cf31e01, so they are not introduced here and are outside C1.8's scope.
- runtime.mode's non-hook consumers (config print, doctor's scope.mode row, the daemon's contract monitor) still read config.Load, which clamps an invalid mode to auto. Only the hooks refuse. This split is documented (troubleshooting §6 and safe-disable step 3), and only the hooks admit capture.
- Integrated gates (whole tree, -race everywhere, hosted CI) remain the coordinator's. This seat ran only the touched packages: internal/config, internal/cli, test/docs and tools/devtool.

### Needs the owner

- Q1: On the hook path, a runtime.redact setting (3d3c5bd) or runtime.mode setting (f846f91) that cannot be applied as written now stops all recording instead of falling back to the default. That departs from the documented rule that an invalid value is never fatal. I chose fail-closed for both because each fallback records something the operator did not allow: redaction weaker than configured, or recording while they were switching it off. The cost: a typo in either key stops recording until fixed, reported as config.capture critical by self-test and doctor and logged in hook-quiet. Reversing either is one predicate in internal/config/capture_load.go (inCapturePolicy or inCaptureSwitch, plus the post-fallback comparison), and the tests pin whichever way it is set.
- Q2: Implementer's criterion change 20fce35: self-test under the config-corrupt fault now exits 1, not 0 (TestSelfTest_ConfigCorruptFallsBackButReportsTheRefusedCapture), because a truncated config.json means every hook records nothing. The owner should know that a truncated config is now a failing self-test.


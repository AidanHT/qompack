# Evaluation harness workstream report — C5.4

Branch `closeout/eval`. Workflow `wf_16dd5d95-b3a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `blocked`, head `cf31e01`

### Summary

I am the duplicate C5.4 eval instance, not the original, and I have stopped work. My agent id is a21303f8a96aa706f and ListAgents shows it started around 17:31. The original is abae690d6d2302b0a, started around 17:20. The coordinator said the copy must stand down and the original owns qompack-cx-eval.

I first got this wrong. The earlier history in my context was inherited from the original, so I told the coordinator I was the original. I found out when a message I sent to a21303f8a96aa706f was delivered back to me, and I have sent the coordinator a correction.

Before I realised, I created five files in internal/eval: livestream.go, liveaccount.go, livetask.go, livecheck.go and livetrial.go. They redeclare the types in the original's live_types.go, so internal/eval does not compile while both sets exist. I also copied the smoke evidence into plans/sdd/V6-closeout/eval/runs/. That covers smoke1-stock-compact/, smoke2-plugin-dir-compact/ and smoke_driver.py; the original had already run those sessions before I started. I have not deleted or moved anything, including the original's files, and I made no commits.

I did stop one process, PID 21172. It was the qompack daemon for the smoke2 scratch project, and I checked its command line before stopping it. That process was started by the original's smoke session, not by me, so it was outside the rule to stop only processes I launched.

There is one product finding the owner should see, from the original's smoke2 session. Claude Code 2.1.280 rejects Qompack's PreCompact hook output: it reports "Hook JSON output validation failed", and a hookSpecificOutput carrying hookEventName PreCompact and customInstructions is not accepted. That error text is then fed into the post-compaction context. Headless /compact in streaming-input mode did work, and the SessionStart:compact rehydration block was injected.

### Open issues

- Five untracked files I created as the duplicate collide with the original's live_types.go and break the internal/eval build: internal/eval/livestream.go, liveaccount.go, livetask.go, livecheck.go and livetrial.go (all under C:/Users/Quant/Documents/Programming/Projects/qompack-cx-eval). The original instance or the coordinator should decide whether to delete them.
- I copied smoke evidence into C:/Users/Quant/Documents/Programming/Projects/qompack-cx-eval/plans/sdd/V6-closeout/eval/runs/ (smoke1-stock-compact/, smoke2-plugin-dir-compact/, smoke_driver.py). It is untracked; the original may keep or replace it.
- Claude Code 2.1.280 rejects Qompack's PreCompact hookSpecificOutput (hookEventName PreCompact with customInstructions), and the validation error is fed into the post-compaction context. The evidence is runs/smoke2-plugin-dir-compact/stream.jsonl, the local-command-stdout line of turn 1.
- `--plugin-dir` sessions leave ~/.claude/plugins/data/qompack-inline behind in the global config. It is shared with other agents; I did not delete it.

### Needs the owner

- Coordinator or the original instance (abae690d6d2302b0a): remove or keep my five colliding internal/eval/live*.go files.
- Product owner: the PreCompact hook output is rejected by the current host (Claude Code 2.1.280). Decide whether to stop emitting hookSpecificOutput for PreCompact.

## Independent review

### review:eval:both: unsound

- **blocker** `C:/Users/Quant/Documents/Programming/Projects/qompack-cx-eval (git log cf31e01..HEAD)` — Nothing in C5.4 was delivered. HEAD is still cf31e01 with no commits. eval.LiveRunner is still nil (types.go and replay.go are unmodified), there is no `devtool live-eval` subcommand (tools/devtool is untouched), and there is no task-set file. plans/sdd/V6-closeout/eval/preregistration.md does not exist, and there are no parsing/accounting unit tests with recorded fixtures. The only output is untracked, uncompiled code and two copied smoke runs. The implementer reports status 'blocked' because it was a duplicate instance and stood down.
  - Evidence: `git rev-parse HEAD` = cf31e01f38f3d3ba4afd96c4c75db3e4b87d54cd. `git status --short` shows only untracked internal/eval/live*.go and plans/sdd/V6-closeout/. No preregistration.md or task-set file exists under plans/sdd/V6-closeout/eval, and there are no *_test.go files for the live code.
  - Fix: Treat C5.4 as not done. The original instance (abae690d6d2302b0a) or a fresh run must deliver all five items with commits: LiveRunner, the devtool entry point, the 8-12 task set, preregistration.md written before any trial, and fixture-based tests behind an env gate. It must then come back for re-review.
- **blocker** `internal/eval/livetask.go:30-137 vs internal/eval/live_types.go:68-163` — The internal/eval package does not compile. The duplicate's five files (livestream.go, liveaccount.go, livetask.go, livecheck.go, livetrial.go) redeclare the original's types and constants from live_types.go. Any build, test or merge of this worktree fails, including the existing replay, score and ledger tests in the package.
  - Evidence: `go vet ./internal/eval/` reports: OutcomeConstraint, OutcomeRecovery, CheckFileExists, CheckFileAbsent, CheckFileUnchanged, CheckCommand, LiveTaskSet, LiveTask, LiveStep and LiveCheck are each redeclared (livetask.go:30..137 against live_types.go:68..163), then 'too many errors'.
  - Fix: The coordinator or the original owner keeps one set. Most likely that means deleting internal/eval/{livestream,liveaccount,livetask,livecheck,livetrial}.go, the duplicate's files, and keeping live_types.go and live_stub_*.go (mtimes 17:40-17:41, the original's). Then confirm `go vet ./internal/eval/` and `go test ./internal/eval/` pass before any commit.
- **major** `internal/hookio/output.go:55 (PreCompactOutput) and internal/hookio/output.go:25 (CustomInstructions field)` — This is a confirmed product defect in the core Qompack claim, not in this workstream's scope. Claude Code 2.1.280 rejects Qompack's PreCompact response: hookSpecificOutput with hookEventName "PreCompact" is not in the host's accepted enum. The customInstructions are therefore dropped, and the validation error text, including the hook's output, goes into the post-compaction context. The rehydration path (SessionStart:compact) did inject, so only the PreCompact channel is broken.
  - Evidence: plans/sdd/V6-closeout/eval/runs/smoke2-plugin-dir-compact/stream.jsonl contains: 'Hook JSON output validation failed — hookSpecificOutput.hookEventName: expected one of "PreToolUse" | "UserPromptSubmit" | "UserPromptExpansion" | "SessionStart" | "Setup" | "PreModelSwitch" | … The hook's output was: {"hookSpecificOutput": {"hookEventName": "PreCompact", "customInstructions": "Encode what a competent engineer ...'. The session ran claude-haiku-4-5-20251001 with --plugin-dir and --include-hook-events against the eval.smoke bundle.
  - Fix: Route this to the product owner and the hookio/checkpoint owners. Check the current PreCompact output contract in the Claude Code hooks docs. If PreCompact takes no hookSpecificOutput, stop emitting it from PreCompactOutput and pass the focus instructions through whatever channel the host documents (or none). Add a host-contract test that checks each emitted hookEventName against the host's accepted set, and re-run a real compaction smoke to confirm the error text no longer reaches the context.
- **minor** `plans/sdd/V6-closeout/eval/runs/ (smoke1-stock-compact/, smoke2-plugin-dir-compact/, smoke_driver.py)` — The smoke evidence is untracked, and the duplicate copied it without recording provenance. The driver configs and meta.json also embed absolute scratchpad paths from another session, which will not resolve. Nothing in the evidence records whether the user's global ~/.claude settings were snapshotted and verified unchanged, which the task required ('never leave the user's global settings modified — snapshot and verify them').
  - Evidence: driver-config.json and meta.json reference C:/Users/Quant/AppData/Local/Temp/claude/.../scratchpad/smoke2/... and the bundle path. smoke_driver.py has no settings snapshot or verify step (grepping for settings/snapshot finds only the --setting-sources arg).
  - Fix: When the original commits the evidence, add a README in runs/ covering: who ran each session and when, the CLI version, the model, and the bundle build command. Add a before/after hash of the global ~/.claude settings and plugins files to each run's meta, and make the live driver fail closed if they differ.
- **minor** `process conduct (PID 21172)` — The duplicate stopped a process it did not start: the smoke2 qompack daemon launched by the original's session. The shared-machine rule forbids this. It is harmless here because that was a smoke daemon, but it could have corrupted a live capture the original was inspecting. The implementer does disclose it.
  - Evidence: Implementer summary: 'I did stop one process, PID 21172 ... That process was started by the original's smoke session, not by me.'
  - Fix: No code fix. The coordinator should tell the original instance that its smoke2 daemon was killed, in case it depended on that daemon's state or on .qompack/ in the smoke2 scratch project.
- **nit** `implementer open_issues: ~/.claude/plugins/data/qompack-inline` — The claim that `--plugin-dir` leaves ~/.claude/plugins/data/qompack-inline behind is stale or unverified. The directory is not there now, so something else removed it or it never persisted. The live driver still does not check the global plugin-data directory after a --plugin-dir run, so the question stays open.
  - Evidence: `ls ~/.claude/plugins/data/` currently lists only rust-analyzer-lsp-claude-plugins-official and superpowers-claude-plugins-official.
  - Fix: In the live driver, snapshot ~/.claude/plugins/data before and after each --plugin-dir trial. Record any residue it creates and remove it, touching only entries that did not exist at the snapshot.

## Fix seat (review resolution) — status `partial`, head `4421f1871be24eb070d95989efe3edfbf10a5068`

### Root cause

A coordinator SendMessage at about 17:31 resumed a second copy of the eval agent in the same worktree. The two copies wrote conflicting, uncommitted versions of internal/eval: a five-file implementation, and live_types.go plus ErrNotImplemented stubs. The versions redeclared 10 identifiers, so the package did not compile, and the implementer seat stood down with nothing committed. Following COORDINATOR-DECISION.md, I preserved the stopped copy's files as .go.txt evidence, kept and completed the implemented set, and built all five C5.4 deliverables from there. Separately, a product defect found through the harness: PreCompactOutput (internal/hookio/output.go:58-59) emits hookSpecificOutput with hookEventName "PreCompact", which Claude Code 2.1.280's hook schema rejects. The rejection was observed in smoke2, pilot1 and pilot2, and is routed to C1.12.

### Summary

C5.4 is delivered in 13 commits on closeout/eval (base cf31e01, HEAD 4421f18). The one deliverable missing is report.md: the harness refused the write ("Subagents should return findings as text, not write report files"), so the report's content is below and in open_issues. Status is "partial" because of that missing file and because the confirmatory run is the coordinator's (C5.5). I did not use parallel subagents: this seat had no tool to spawn them. I used parallel tool calls and background test runs instead.

ROOT CAUSE (why the implementer seat produced nothing): at about 17:31 a coordinator SendMessage resumed a second copy of the eval agent in this worktree. The two copies wrote incompatible, uncommitted packages:
- one wrote livestream/liveaccount/livetask/livecheck/livetrial.go, an implementation;
- the other wrote live_types.go plus two stub files that return ErrNotImplemented.
The two sets redeclared 10 identifiers, so `go vet ./internal/eval/` failed and nothing was committed. COORDINATOR-DECISION.md identifies the live_types.go family as the stopped copy's. I moved it, byte-identical, to plans/sdd/V6-closeout/eval/runs/duplicate-instance-files/*.go.txt (sha256 91161e7b…, bd09caea…, d24a2510…) and kept and finished the implemented set.

WHAT WAS BUILT:
(1) Live driver, `go run ./tools/devtool live-eval --tasks F --arms stock,qompack --trials N --model M --out D --install plugin-dir|marketplace --bundle B [--include-held-out --only --max-sessions --dry-run --keep-raw-transcripts]`, in tools/devtool/liveeval.go and liveeval_host.go:
- Each trial is one `claude -p --input-format stream-json --output-format stream-json --verbose --include-hook-events` process, with a driver-chosen --session-id, --setting-sources project,local, --permission-mode dontAsk and the task's allowedTools. ENABLE_CLAUDEAI_MCP_SERVERS=false is set, and the project is a disposable git repository copied from the fixture.
- The driver sends one message per step and waits for each step's result line before sending the next.
- The plugin loads by --plugin-dir (verified in 2.1.280 --help), or through the real flow: `claude plugin marketplace add <dir> --scope local`, then `plugin install qompack@qompack-live-eval --scope local -y`, reversed afterwards by uninstall and marketplace remove.
- The trial daemon is stopped through the product's admin.shutdown IPC request, never killed.
- A guard hashes ~/.claude/settings.json, plugins/installed_plugins.json and plugins/known_marketplaces.json, and records which plugins/{cache,marketplaces,data}/qompack* directories exist, before and after every trial. If anything differs, the run stops and the trial record says what changed.
- Nothing runs without QOMPACK_LIVE_EVAL=1.

(2) Forced compaction headless:
- A /compact user turn in streaming-input mode compacts deterministically at that step.
- Automatic compaction can also be forced, with CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000 and CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=30. The CLI's own --autocompact flag has a minimum of 100k.
- The full chain was seen in smoke2, pilot1 and pilot2: PreCompact wrote checkpoint 0001, SessionStart:compact fired, the host recorded a hook_additional_context with `<!-- qompack:injected seq=1`, and the model answered after compaction.
- Smoke3 shows a checkpoint is written even when the host refuses the compaction (reason too_few_groups).

(3) eval.LiveRunner: HostForkRunner in internal/eval/livefork.go renders the kept blocks as the post-compaction prompt and runs one real session through a HostSessionRunner seam (internal/eval may not import os/exec). It maps the session's main-loop requests onto Actions. Replay's live mode now forks each compaction event and splices what the model did into the window (at, min(at+K, next at)), replacing the modelled repairs. The QOMPACK_EVAL_LIVE gate and the no-runner refusal are unchanged.
The pure layer beside it:
- stream and transcript parsers. The transcript parser keeps no session_context, prompt_snapshot or message bodies.
- accounting that differences running modelUsage totals and never sums them. It takes a resumed-session baseline, deduplicates requests by message id, splits thinking from output, and leaves the compaction's cache-write TTL unknown.
- a dated multi-model rate table. The claude-haiku-4-5 row matches the host's own costUSD within rounding, and a test pins it.
- grading through os.Root.
- Wilson and Newcombe intervals, and DecideLive.
- hook-failure detection from the stream, the transcript and stderr, with one failure seen by several sources counted once.

(4) Task set and pre-registration:
- testdata/eval/live/tasks.json has 10 tasks: 7 visible and 3 held out, 2 of them changing-requirement. It comes with Go fixtures, hidden test suites copied in after each session, reference solutions and a disjoint pilot set.
- plans/sdd/V6-closeout/eval/preregistration.md was committed at 31b1d40, before any pilot. It fixes: the model claude-sonnet-5 with a pre-set contingency; 40 sessions (10×2×2) and their precision (about ±0.27 at p≈0.7, so only large effects are detectable); the Newcombe non-inferiority rule with a 0.20 margin; intention-to-treat and the re-run policy; the task-set hash 14e9ee33…; and the preconditions that C1.12 and C1.1 are fixed.

(5) Unit tests use recorded fixtures only: the 3 real smoke streams and a redacted real transcript. The largest is TestLiveTaskSet_ReferenceSolutionsPassEveryCheck: all 11 tasks are gradeable, because each reference solution passes every check and each untouched fixture fails at least one.

TESTS:
- Windows (shared machine under load from other agents):
  - `go vet ./internal/eval/ ./tools/devtool/` exit 0; `devtool fmt-check` exit 0.
  - `go test ./internal/eval/ -count=1 -timeout=30m` ok (8.9s).
  - `go test ./tools/devtool/ -count=1 -timeout=30m` ok (104.9s at ecbdd74); later devtool fixes were run focused, ok.
  - `devtool lint --only=golangci-lint,nomagic,importgraph,sleepcheck` at HEAD: all PASS.
  - Full `devtool lint`: bindeps FAIL (4 violations, golang.org/x/sys/unix). The same failure reproduces on the untouched base cf31e01, so it predates this branch. stubskips FAIL: 8 problems, all in packages this branch does not touch (daemon, hookio, mcp, store, e2e, fault, integration); most are 30-minute timeouts on the loaded machine.
  - Note: stubskips runs the whole test tree by design. I ran the full lint once, as instructed, and that conflicts with the "do not run the whole tree" rule.
  - Per-commit `go vet` on all 11 code commits: exit 0 (bisectable).
- Linux (container qompack-v6-linux-verification, Go 1.26.6, root, GOMAXPROCS=4, commit 40657c1 in /work/cx-eval-40657c1-20260922T230438Z): vet exit 0; eval ok (0.9s); devtool ok (8.3s); a focused -v run of the live tests gave 57 PASS and 0 FAIL, including all 11 reference-solution subtests.
- No timing test failed on either OS, so no co-load re-run was needed. Logs are in plans/sdd/V6-closeout/eval/runs/tests/.

REAL SESSIONS: six in total, which is the workstream cap. All used claude-haiku-4-5-20251001 and were agent-executed under decision D3.
- smoke1, smoke2 and smoke3 were run by the implementer.
- Pilots 1–3 were run by me on the pilot task, after the pre-registration commit:
  - pilot1: qompack arm, plugin-dir;
  - pilot2: qompack arm, marketplace;
  - pilot3: stock arm.
- All three completed with task success, no violations, recovery, and the guard unchanged. Both qompack pilots recorded the C1.12 PreCompact rejection.
- A marketplace rehearsal with no model call found one leak: after uninstall, Claude Code keeps an .orphaned_at copy in plugins/cache/<marketplace>. Fixed in 09a5222. It also showed that another close-out lane creates and removes plugins/data/qompack-inline on the same machine; hardened in 4eee177.

CRITERION CHANGES: none. No assertion, threshold or golden was touched. The existing live-mode refusal tests are unchanged and pass.

DECISIONS ALTERED:
- Replay live mode now uses the runner, as described in (3).
- Ruling R7-2 ("eval.LiveRunner ... deliberately left unwired") is superseded for the close-out by D3 and C5.4. The shipped binary still installs no runner. The note in releasescopereport.go and the SP17-M7-07 row of docs/release.md are now stale; that is left to C6.1.

REVIEW RESOLUTION:
- (1) Blocker "nothing delivered": confirmed. Resolved by the 13 commits above.
- (2) Blocker "package does not compile": confirmed. Resolved, but I kept the other set from the one the reviewer suggested. The coordinator's decision file names live_types.go as the stopped copy's, and that set was only stubs. It is preserved, not deleted.
- (3) Major, PreCompact hookSpecificOutput rejected: confirmed independently in smoke2, pilot1 and pilot2. PreCompactOutput (internal/hookio/output.go:58-59) emits hookEventName "PreCompact", and the host's schema has no PreCompact entry. This is routed to C1.12 (the packaging lane) and not fixed here, to avoid colliding with that lane. The harness now detects it on every /compact trial and names the trials in the summary. It cannot detect it on automatic compaction, because the host prints nothing there (smoke3). A C1.12 fix is a pre-registered precondition.
- (4) Minor, provenance and settings guard: confirmed. runs/README.md records provenance and says smoke1 and smoke2 had no snapshot and smoke3's check result was not kept. The driver's guard now fails closed and is tested.
- (5) Minor, PID 21172 stopped by the duplicate: confirmed. There is no code fix. The smoke2 evidence was captured before the kill and is intact. This seat stopped only processes it started (its own background shell); trial daemons are stopped through admin.shutdown.

### Commits

- 5b6c073 test(eval): add the frozen live task set, fixtures and rates
- 3d986ae feat(eval): parse and account real headless host sessions
- 75c57d8 feat(eval): grade live trials and summarize them per arm
- 715a614 feat(eval): implement eval.LiveRunner with a host fork runner
- ecbdd74 feat(devtool): add live-eval, the real-host trial driver
- 31b1d40 docs(eval): pre-register the qompack-live-v1 evaluation
- 251ec8b docs(eval): preserve the C5.4 smoke sessions and duplicate files
- 09a5222 fix(devtool): remove the orphaned cache a marketplace trial leaves
- 9d5efc6 fix(devtool): record a run with no plugin arm as installing nothing
- 4eee177 fix(devtool): never remove a plugin data dir the trial did not make
- 40657c1 test(eval): check every type assertion in the live tests
- a771f82 docs(eval): record the marketplace rehearsal and three pilot trials
- 4421f18 docs(eval): keep the fix seat's Windows and Linux test logs

### Tests

- `go vet ./internal/eval/ ./tools/devtool/ (Windows, HEAD)` — exit 0
- `go run ./tools/devtool fmt-check` — exit 0
- `go test ./internal/eval/ -count=1 -timeout=30m (Windows)` — ok 8.882s (runs/tests/eval-full-windows.txt)
- `go test ./tools/devtool/ -count=1 -timeout=30m (Windows, at ecbdd74)` — ok 104.854s (runs/tests/devtool-full-windows.txt)
- `go test ./tools/devtool/ -run 'TestRemoveCreatedPluginData|TestRunLiveEval|TestRemoveOrphaned' -count=1 (Windows, after 4eee177)` — ok
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,sleepcheck (Windows, HEAD)` — all PASS, exit 0 (runs/tests/devtool-lint-head-windows.txt)
- `go run ./tools/devtool lint (Windows, full)` — exit 1: bindeps 4 violations (golang.org/x/sys/unix; the same failure reproduces on base cf31e01) and stubskips 8 problems (all in untouched packages: daemon, hookio, mcp, store, e2e, fault, integration; mostly 30m timeouts under co-load); golangci-lint findings from that run fixed in 40657c1; nomagic, importgraph, testdeps, sleepcheck, runpatterns, docmarkers, coveragefloors PASS
- `go run ./tools/devtool lint --only=bindeps on git archive of cf31e01` — the same 4 violations: pre-existing
- `go vet ./internal/eval/ ./tools/devtool/ at each of the 11 code commits (git archive)` — all exit 0 (runs/tests/per-commit-vet-windows.txt)
- `Linux container, GOMAXPROCS=4, 40657c1: go vet ./internal/eval/ ./tools/devtool/` — exit 0
- `Linux: go test ./internal/eval/ -count=1 -timeout=30m` — ok 0.893s
- `Linux: go test ./tools/devtool/ -count=1 -timeout=30m` — ok 8.279s
- `Linux: go test ./tools/devtool/ ./internal/eval/ -run 'Live|Host|Fork|Splice|Merge|Estimate|Account|Ledger|Summarize|Decide|Grade|ToolUses|ParseHook|RemoveCreated|RemoveOrphaned|ReferenceSolutions' -v` — 57 PASS, 0 FAIL, including all 11 reference-solution subtests (runs/tests/linux-40657c1/)
- `QOMPACK_LIVE_EVAL=1 go run ./tools/devtool live-eval --tasks testdata/eval/live/pilot.json --arms qompack --install plugin-dir --bundle <bundle> --max-sessions 1` — real session: completed, task success, 0 violations, recovered, guard unchanged, C1.12 recorded (runs/pilot1-plugin-dir)
- `same with --install marketplace` — real session: completed, success, orphaned cache removed, guard and an independent fingerprint unchanged (runs/pilot2-marketplace)
- `same with --arms stock` — real session: completed, success, guard unchanged (runs/pilot3-stock)
- `go run ./tools/devtool live-eval ... without QOMPACK_LIVE_EVAL` — refused, as designed

### Criterion changes

- None. No existing test criterion, threshold, golden or assertion was changed. TestReplay_LiveModeRefusedWithoutEnv and TestReplay_LiveRunnerAbsent are unchanged and pass.

### Open issues

- The report file plans/sdd/V6-closeout/eval/report.md was NOT written or committed. The harness blocked the Write ('Subagents should return findings as text, not write report files'), so its content, including the Review resolution section, is in this summary. The coordinator should commit it from here.
- The confirmatory run (C5.5, 40 sessions) has not been executed. Run the pre-registration's §9 command on a frozen candidate where C1.12 and C1.1 are fixed: go run ./tools/devtool bundle --target windows/amd64 --version <v> --out dist/live-bundle; QOMPACK_LIVE_EVAL=1 go run ./tools/devtool live-eval --tasks testdata/eval/live/tasks.json --include-held-out --arms stock,qompack --install plugin-dir --bundle dist/live-bundle/qompack-plugin-<v>-windows-amd64 --max-sessions 40.
- C1.12: Claude Code 2.1.280 rejects the PreCompact hookSpecificOutput built at internal/hookio/output.go:58-59. This is routed to the packaging lane, not fixed here. The harness detects it only on /compact turns; on automatic compaction the host prints nothing.
- `qompack eval --json` still exits 1: commands.Deps.EvalArtifacts is nil, and a live LiveSummary does not map naturally to the replay-shaped EvalInput.
- Ruling R7-2's text ('deliberately left unwired') in tools/devtool/releasescopereport.go and in the SP17-M7-07 row of docs/release.md is now stale. It is left to the docs lane (C6.1).
- Replay: the live fork window (at, at+K] covers K turns, while the pre-existing deterministic Demands window [at+1, at+K) covers K-1. This off-by-one predates this branch and was left unchanged.
- Shared machine: other lanes' --plugin-dir sessions create and remove ~/.claude/plugins/data/qompack-inline. A concurrent change stops a live-eval run, which fails closed. Live trials should not run while another lane runs plugin sessions.
- HostForkRunner has no devtool corpus entry point (such as `replay --live`). It is complete and tested through the Go API; that entry point belongs with C5.3 if it is wanted.
- The pre-existing bindeps failure (golang.org/x/sys/unix in the linux/darwin shipped binary) reproduces on base cf31e01. Full-lint stubskips reported problems in untouched packages: whole-tree timeouts plus two skip-reason violations, in internal/daemon TestDeliveryPath_V6_RefusesAliasedSegmentParent and internal/hookio TestCaptureScope_JunctionSwapDefeatsLexicalContainment.
- The pre-registration's §2 says pilot directories are named 'runs/pilot-*'. The actual names are pilot1-plugin-dir, pilot2-marketplace and pilot3-stock. The wording is cosmetic; the document itself is unchanged.

### Needs the owner

- The model for the confirmatory run: claude-sonnet-5 is pre-registered, with a contingency to fall back to the 'sonnet' alias. Any change must be recorded as an amendment before the first confirmatory trial.
- Whether the in-plugin `qompack eval` should read live-eval summary.json (a new EvalArtifacts provider plus a report mapping), or stay replay-only and keep exiting 1.
- The confirmatory budget: 40 sessions (10 tasks × 2 trials × 2 arms) out of decision D3's 40–80, shared with UAT. A second batch of 40 is allowed only if recorded before the first batch's outcomes are read.


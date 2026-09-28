# Wave 2b workstream report: w2-eval2 (C5.4)

Branch `closeout/w2-eval2`. Workflow `wf_b2b236ea-ef1`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `c2e6b1bfb58267af380a16fe3b5b5dedf46757e3`

### Root cause

The wave-1 eval harness was written after its reviewer had looked at an empty branch, so nobody had checked it against real artifacts or against its own pre-registration. Wiring the eval artifacts provider exposed a latent defect in the eval command: it picks the first non-baseline policy, and on a real replay report that is the null reference, so qompack eval failed on it. The driver never absolutized its paths, so the pre-registered command, which names the bundle relative to the repository, would have loaded no plugin in the trial project. The analysis had edge cases where one-sided handling favoured the plugin: no reported plugin state voided the verdict, and archive lookups counted as re-derivation. It also accepted any materials under the pre-registered id, gave a verdict for runs the guard stopped, and let the operator's QOMPACK_* environment reach one arm.

### Summary

C5.4 completion (workstream w2-eval2). Branch closeout/w2-eval2 in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w2-eval2, base closeout/integration b070bbe, HEAD c2e6b1b. The tree is clean and nothing was pushed.

## What I found when I resumed

The brief said the earlier attempt committed nothing. That was not the case. A 2026-09-25 wave-2b attempt had already committed 13 commits (0722e3f..a9c3cd3), with red logs red-01..red-10 under plans/sdd/V6-closeout/w2-eval2/runs/. It also left one uncommitted edit, which did not compile: the file_lines line counter in internal/eval/livecheck.go had a raw newline inside a rune literal.

I reviewed every one of those commits against the code, the preregistration and the pilot evidence:
- **Adopted as they are:** 0722e3f, 176143a, 1c0e5ae, e346dde, 2e3ae56, 47d5434, 2af223c, 4d7028a, 3120088, a9c3cd3.
- **Adopted, then revised by later commits:** 20e3460 (by 07d52b6), 29c1ad4 (by a81d63b, c399231, 6a00064) and 52dc908 (user-guide text).
- **Discarded:** nothing.
- **Uncommitted edit:** the bufio.Scanner under-count it fixes is real (red-11 shows "1 non-empty line(s), want 4"). I repaired the literal and committed it as 85db936.

No process referenced the worktree when I started. The last edits were ten minutes old, so the earlier attempt was not still running.

## (1) Independent adversarial review: defects found and fixed

Every fix started with a failing test. The red logs are runs/red-11..red-21.

1. **qompack eval judged the wrong replay policy** (a81d63b, red-12). `primaryPolicy` picked the alphabetically first non-baseline policy.
   - The replay driver's default run scores stock, null, oracle and qompack-rehydrate, so the command reported **null**. Null keeps nothing and re-attempts every eliminated approach by construction.
   - Reproduced on a real report: `go run ./test/replay --out <scratch>/bench-replay.json`, then `qompack eval --corpus` on it, gave `policy: null`, REC-02 = 24, FAIL, exit 1.
   - Wiring the provider made this the answer in every checkout.
   - **Fix:** the gates are now the qompack-* policy's. The reference policies are named in a note and never judged, and a report with no Qompack policy is inconclusive. The same real report now reads qompack-rehydrate, PASS.

2. **A relative `--bundle` would load no plugin** (4a39d1b, red-13).
   - The driver passed `--bundle` through unchanged, but each session runs in its temporary project directory.
   - The preregistration's §9 command uses a repo-relative bundle path. The host would resolve `--plugin-dir` against the trial project and load nothing, every qompack trial would be a plugin mismatch, and the whole run would be not-applicable. A relative `--claude` path had the same fault.
   - **Fix:** both are made absolute. The dry run now prints each arm's resolved host command line and the marketplace install commands.

3. **Trial projects leaked into the shared temp directory** (d774b96, red-14). `os.MkdirTemp("")` made every offline test leave a qompack-live-* project in %TEMP%; 80 had built up. Work directories are now made under a `liveEnv.workRoot`, which tests set to their own temporary directory.

4. **"Confirmatory" never checked the frozen materials** (c399231, red-15).
   - A run of an edited tasks.json or edited fixtures under the same task-set id, or one that used the marketplace install, would have been judged as the preregistered study.
   - **Fix:** a new `eval.LivePreregistrations` records the task-set sha 14e9ee33…, fixture tree 30cf769d… and install plugin-dir. A test pins those values to preregistration §2, amendment A1, §3 and the committed files. `notConfirmatory` checks all three.

5. **One infrastructure failure on a qompack trial voided the whole verdict** (07d52b6, red-16, preregistration amendment A2).
   - A qompack trial whose host never started (for example a failed install) counted as a plugin mismatch, which makes the verdict not-applicable. The same failure on the stock arm counted as a stock failure. That asymmetry can only ever spare the plugin.
   - **Fix:** trials now record `host_reported_plugins`. A mismatch needs a reported plugin list; otherwise the trial is a harness failure and fails every outcome.

6. **Plugin archive lookups counted as re-derivation** (4f10acb, red-17, amendment A3).
   - The no-rerun `tool_not_used_after` check matched the plugin's own MCP tools too. A qompack `recall` query naming "go run ./cmd/probe" failed the constraint for doing the recovery the plugin exists to provide. The stock arm has no such tools, so this only ever counted against Qompack, in H2.
   - **Fix:** that check now ignores `mcp__plugin_qompack_qompack__*` calls. A Bash run of the same command still fails it.

7. **A run the guard stopped still wrote a verdict** (2a4d1cb, red-18, amendment A4). The verdict was computed over a subset of the planned trials. `LiveSummary.StopEarly` now makes such a run not-applicable and says how many of the planned trials ran.

8. **Hook latency did not match preregistration §6** (97f2557, red-19). The summary pooled pipe-observed latency across all hooks, and summary.md printed none. §6 asks for host-measured latency per hook. `ArmSummary.HostHookMS` now gives each hook's transcript durationMs (runs, p50, p95, max), and summary.md prints it as a table.

9. **Operator QOMPACK_* variables reached the trials** (ba9f16e, red-20). The session environment was `os.Environ()` plus the driver's settings, so an operator's kill switch or mode would silently change the plugin under test on the qompack arm only. Inherited QOMPACK_* variables are now removed; invocation.json and the trial notes record the names, never the values.

10. **A newer unfinished run was invisible** (6a00064, red-21). A newer run with a plan.json but no summary.json was skipped silently, so an older run was presented as the latest. The provider still reports the newest finished run, and now names any newer unfinished run in a note.

**Verified and left unchanged:**
- **Accounting:** running totals are differenced, never summed, and there is no double count. On pilot1, per-category sums equal the host's final modelUsage exactly: input 1523, output 854 + thinking 754 = 1608, cache read 114764, cache write 12071 (1h) + 149 of unknown TTL = 12220. The compaction request is attributed "beside" as compaction, and its TTL is honestly unknown, so the estimate is marked a lower bound. That lower bound (0.045181) is 149 × 1.25e-6 below the host's own 0.04536765, which cross-checks the rate table.
- **Rate table:** dated 2026-09-22 and labelled an estimate. Every price and multiplier matches the claude-api skill bundled with 2.1.280 (cached 2026-06-24): haiku-4-5 1/5, sonnet-5 2/10, sonnet-4-6 3/15, opus-5 5/25, opus-5-5 4/20 with 0.05x cache reads, opus-4-8 5/25, fable-5 and fable-5-1 10/50 with fable-5-1 at 0.025x; cache writes 1.25x for 5 minutes and 2x for 1 hour.
- **Grading:** hidden tests are copied in after the session and after the daemon stops. Reference solutions pass every check.
- **Guard and cleanup, stock arm, stream driving:** the guard hashes settings.json and the two plugin registries and records every plugins/{cache,marketplaces,data}/qompack* directory. `--setting-sources project,local` keeps the operator's plugins out of both arms. The stock arm flags Qompack from any source and names any foreign plugin. Stream driving waits for each turn's result line before sending the next message.
- **Statistics:** Wilson and Newcombe method 10 formulas are correct, the published 56/70 vs 48/80 example is pinned by a test, and the decision rule matches §8.
- **Preregistration frozen:** I re-derived the hashes independently in Git Bash. tasks.json 14e9ee33, rates.json 97dfb469 and pilot.json 4da30ddf match §2. `find fixtures hidden -type f | LC_ALL=C sort | xargs sha256sum --text | sha256sum` gives 30cf769d…, matching A1.

## (2) qompack eval wiring

The provider is `FileEvalArtifacts`, installed in the CLI (4d7028a). It reads the newest finished run under dist/live-eval and testdata/bench-replay.json, or whatever `--corpus` names. Each live run is reported with its qualification first: who executed it (agent-executed, under D3), the model and the preregistered model, the bundle, the task-set and fixture-tree hashes, the sample size and 95% intervals. It also says whether the run is confirmatory and, if not, every reason.

Three gates come from the live run:
- **LIVE-T01** follows the preregistered decision.
- **LIVE-T02** fails only on the H2 regression.
- **LIVE-R01** is reported and never judged.

A project with no artifacts reports unavailable, names both paths it looked in, exits 1, and creates nothing. The user guide was updated alongside each change; commands.md and the plugin golden were regenerated.

## (3) Stale text

The R7-2 note in the release-scope report and the SP17-M7-07 row of docs/release.md are fixed (3120088). The row matches the generator's output exactly. I retired the same note from the nightly.yml comment (f1e4cc5).

The live-window off-by-one is documented and pinned by a test (a9c3cd3). Live mode splices over the scoring horizon (at, at+K]. The deterministic demand window is (at, at+K), one turn shorter. I verified this against `Demands` and `horizonActions`. Widening the demand window would move FractionOfOPT and the committed phase-0 baseline, so that is left for the owner.

## (4) Dry run, no model calls

- **Bundle:** assembled from 109d675 on a clean tree: `go run ./tools/devtool bundle --target windows/amd64 --version 0.3.0-eval2.dryrun --out dist/live-bundle`, dirty=false, BUNDLE.json sha256 72f82de4….
- **§9 command with `--dry-run`:** task set qompack-live-v1 (14e9ee33ccff), fixture tree 30cf769d2437, 40 trials on claude-sonnet-5, arm order alternating. Each arm's host command line is printed, with the relative `--bundle` resolved to an absolute `--plugin-dir`.
- **Marketplace flow:** its install commands resolve in the dry run.
- **Gate:** without QOMPACK_LIVE_EVAL=1 the driver refuses before creating anything.
- **Bundled binary:** `qompack eval` reports the pilot run as non-confirmatory with its reasons, reads a real replay report as qompack-rehydrate PASS, and is unavailable (exit 1) in a project with no artifacts.

Evidence is in runs/dry-run/. No real Claude Code session was started.

## Criterion changes, each with its reason

1. **Which replay policy is judged:** now qompack-*, not the first non-baseline. Null is a reference control, not the product.
2. **Not-applicable narrowed** to trials whose host reported a plugin list (A2). A trial whose host never started is now scored as a failure, which is stricter against the plugin.
3. **`tool_not_used_after` ignores the plugin's own MCP tools** (A3). This removes a false positive that could only ever count against the qompack arm; all other tools still count.
4. **A stopped run is not-applicable** (A4).
5. **"Confirmatory" also requires the preregistered materials and install path** (stricter).

**Test fixture updates:** the `confirmatoryRun` fixture now uses the real preregistered hashes instead of "aaaa"/"bbbb", which the stricter check requires. Test helpers set `HostReportedPlugins: true`.

A2, A3 and A4 were appended to preregistration.md, before any confirmatory trial. No assertion was deleted, no threshold lowered, no golden regenerated to match broken output, and there are no t.Skip or nolint additions.

## Test runs

No wall-clock failure happened, so no co-load re-runs were needed. Logs are in runs/tests/ and runs/linux/.

### Commits

- 0722e3f fix(eval): score a harness failure as failing every outcome [prior 2b attempt, reviewed, adopted]
- 176143a feat(eval): report the H2 regression, variants and task signs [prior, adopted]
- 1c0e5ae fix(eval): count a fresh session's thinking as a known zero start [prior, adopted]
- e346dde fix(eval): match the main loop to its running total across spellings [prior, adopted]
- 2e3ae56 fix(eval): name the fixture tree by a reproducible manifest hash [prior, adopted; hash independently re-derived]
- 47d5434 fix(devtool): guard every qompack plugin directory the host keeps [prior, adopted]
- 20e3460 fix(devtool): count a plugin only from the arm's own install source [prior, adopted; revised by 07d52b6]
- 2af223c refactor(eval): share the live run's plan document with its readers [prior, adopted]
- 29c1ad4 feat(commands): report live and replay evaluation artifacts [prior, adopted; revised by a81d63b, c399231, 6a00064]
- 4d7028a fix(cli): install the eval artifacts provider [prior, adopted]
- 52dc908 docs(commands): describe what qompack eval now reads [prior, adopted; user guide revised later]
- 3120088 fix(devtool): retire the unwired-runner note of ruling R7-2 [prior, adopted]
- a9c3cd3 docs(eval): record why the live window is one turn wider [prior, adopted; windows verified]
- 85db936 fix(eval): count every line of a file_lines check [prior uncommitted edit, repaired and adopted]
- a81d63b fix(commands): judge Qompack's replay policy, not a reference
- 4a39d1b fix(devtool): hand the host an absolute bundle and CLI path
- d774b96 fix(devtool): make trial projects under an environment work root
- c399231 fix(commands): confirm a live run only on pre-registered materials
- 07d52b6 fix(eval): score a trial with no plugin state as a harness failure (+ preregistration A2)
- 4f10acb fix(eval): let an archive lookup recover a fact, not re-derive it (+ preregistration A3)
- 2a4d1cb fix(eval): give a run the guard stopped no verdict (+ preregistration A4)
- 97f2557 fix(eval): summarize hook latency per hook as the host measured it
- ba9f16e fix(devtool): keep the operator's QOMPACK_ variables out of trials
- f1e4cc5 docs(ci): retire the unwired-runner note in the nightly workflow
- 6a00064 fix(commands): name a newer live-eval run that never finished
- 109d675 docs(eval): keep the w2-eval2 package test logs
- c2e6b1b docs(eval): record the live-eval dry run against a 109d675 bundle

### Tests

- `go test ./internal/eval/ -count=1 -timeout=30m (Windows, at 6a00064)` — ok (runs/tests/internal-eval-windows-6a00064.txt)
- `go test ./internal/commands/ -count=1 -timeout=30m (Windows, at 6a00064)` — ok
- `go test ./internal/cli/ -count=1 -timeout=30m (Windows, at 6a00064)` — ok 25.0s
- `go test ./internal/pluginmanifest/ -count=1 -timeout=30m (Windows, at 6a00064)` — ok
- `go test ./tools/devtool/ -count=1 -timeout=30m (Windows, at 6a00064)` — ok 81.6s
- `go test ./test/docs -count=1 (Windows)` — ok
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w2-eval2 --out plans/sdd/V6-closeout/w2-eval2/runs/linux HEAD eval2-touched -- ./internal/eval ./internal/commands ./internal/cli ./internal/pluginmanifest ./tools/devtool ./test/docs (6a00064, uid 10001, -race, GOMAXPROCS=4)` — all PASS: eval 402/0 (1 pre-existing skip TestSynthesize_WriteCorpus), commands 108/0, cli 349/0, pluginmanifest 29/0, devtool 295/0, docs 19/0; go_test_exit=0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,sleepcheck,runpatterns,docmarkers (Windows, 6a00064)` — exit 1: golangci-lint, nomagic, importgraph, sleepcheck, docmarkers PASS; runpatterns FAIL with 26 unsatisfiable patterns, all in other lanes' wave-1 reports (e2e, hostperm, ingest, linux, packaging, rehydrate-cap), unchanged since b070bbe
- `go run ./tools/devtool gen-command-docs --check; gen-config-docs --check; gen-mcp-docs --check` — all up to date, exit 0
- `go run ./tools/devtool release-scope (compare the live-task row with docs/release.md)` — row identical
- `go run ./tools/devtool fmt-check; go vet ./internal/eval/ ./internal/commands/ ./internal/cli/ ./tools/devtool/; go build ./...` — exit 0 at HEAD
- `go test ./internal/commands/ -run TestEval_Replay -count=1 (red before a81d63b)` — FAIL before (runs/red-12), ok after
- `go test ./tools/devtool/ -run TestRunLiveEval_RelativeBundleReachesTheHostWhole -count=1 (red before 4a39d1b)` — FAIL before (runs/red-13), ok after
- `go test ./internal/eval/ -run TestLivePreregistrations -count=1 and go test ./internal/commands/ -run TestEval_LiveNotConfirmatoryUnlessItRanThePreregisteredMaterials -count=1` — FAIL before (runs/red-15), ok after
- `go test ./internal/eval/ -run TestSummarizeLive_NoPluginStateIsAHarnessFailureNotAMismatch -count=1` — FAIL before (runs/red-16), ok after
- `go test ./internal/eval/ -run TestToolUsesAfterSteps_TheArchiveIsRecoveryNotRederivation -count=1` — FAIL before (runs/red-17), ok after
- `go test ./tools/devtool/ -run TestRunLiveEval_StoppedRunReachesNoVerdict -count=1` — FAIL before (runs/red-18: verdict inconclusive), ok after
- `go test ./internal/commands/ -run TestFileEvalArtifacts_NamesANewerRunThatNeverFinished -count=1` — FAIL before (runs/red-21), ok after
- `go run ./test/replay --out <scratch>/bench-replay.json; qompack eval --corpus <scratch>/bench-replay.json` — before a81d63b: policy null, REC-02=24, FAIL, exit 1; after: policy qompack-rehydrate, PASS, exit 0
- `go run ./tools/devtool live-eval --tasks testdata/eval/live/tasks.json --include-held-out --arms stock,qompack --install plugin-dir --bundle dist/live-bundle/qompack-plugin-0.3.0-eval2.dryrun-windows-amd64 --max-sessions 40 --dry-run (bundle from 109d675, clean)` — exit 0: 40 trials, task set 14e9ee33ccff, fixture tree 30cf769d2437, claude-sonnet-5, absolute --plugin-dir printed; no session started (runs/dry-run/)
- `env -u QOMPACK_LIVE_EVAL go run ./tools/devtool live-eval --tasks testdata/eval/live/pilot.json --arms stock --out <scratch>/gate-refusal` — refused, exit 1, no output directory created

### Criterion changes

- qompack eval now judges the qompack-* replay policy, not the alphabetically first non-baseline one. null, oracle and stock are references that bound the metric and are named, never judged. Rationale: null keeps nothing by construction, and judging it failed every real replay report (a81d63b).
- Preregistration A2: a plugin mismatch now needs the host to have reported its plugin list. A trial whose host never started is a harness failure that fails every outcome, which is stricter against the plugin (07d52b6).
- Preregistration A3: a tool_not_used_after check ignores calls to mcp__plugin_qompack_qompack__* tools. Rationale: they look up archived output, run no program and read no file from disk; the stock arm has no such tools, so the old rule could only ever count against the qompack arm (4f10acb).
- Preregistration A4: a run the guard stops before every planned trial has run is not-applicable (2a4d1cb).
- A live run is confirmatory only if its task-set sha, fixture-tree sha and install path (plugin-dir) match eval.LivePreregistrations (stricter; c399231). The confirmatoryRun test fixture now carries the real preregistered hashes instead of placeholder 'a'/'b' strings.

### Open issues

- Task design (not fixed, because it changes the frozen task set): in tool-output-recall and seed-recall the only task check is `go vet ./...`, which the untouched fixture already passes. A session that does nothing scores task success, so 4 of every 20 trials per arm pass the primary outcome regardless, which pushes both rates towards 1 and makes a non-inferior verdict easier. Proposed fix before C5.5: add file_exists task checks for TOKEN and config/seed.go. That needs a new tasks.json hash, a preregistration amendment and an updated eval.LivePreregistrations entry.
- Task design: regression-guard's constraint check 'pinned' (`go test ./list -run ^TestHiddenEvalParseListPinned$`) compiles the hidden JoinList test. A trial that never implemented JoinList therefore also records a constraint violation, so one failure counts in both H1 and H2.
- qompack eval's verdict: any failed live trial (incomplete, max-turns, harness error) makes the overall verdict inconclusive, even for a confirmatory run whose intention-to-treat decision is non-inferior. That is TestEval_LiveFailedTrialsKeepTheVerdictInconclusive, written by the prior attempt. I kept it because it is the conservative choice, but a 40-trial run will almost never read PASS.
- The re-run and sensitivity-analysis policy in preregistration §8 is not implemented in the driver. An infrastructure re-run has to be done by hand as a separate run.
- About 80 qompack-live-* directories accumulated in C:/Users/Quant/AppData/Local/Temp from tests and pilots, not all of them this lane's, before d774b96. I left them in place (C7.6 housekeeping). Real runs still leave each trial's project there by design as evidence; the trial record names it.
- testdata/bench-replay.json is not committed, so in a source checkout the replay half of `qompack eval` appears only after `go run ./test/replay` (or devtool replay) has been run.
- HostForkRunner still has no devtool entry point (such as `replay --live`); it is reachable only through the Go API.
- Pre-existing lint failure: runpatterns reports 26 unsatisfiable -run patterns in other lanes' reports (e2e, hostperm, ingest, linux, packaging, rehydrate-cap). When this report is committed as .md, write its -run patterns with no '|' and without placeholders.

### Needs the owner

- Acknowledge preregistration amendments A2 (a trial with no reported plugin state is scored as a harness failure, not a mismatch), A3 (tool_not_used_after ignores the plugin's own MCP tools) and A4 (a stopped run is not-applicable). All three were appended before any confirmatory trial and are listed under Criterion changes in the summary.
- Decide on the tool-output-recall / seed-recall task-outcome weakness (add file_exists task checks under a new task-set hash, or accept it and document it) before the first confirmatory trial.
- Decide whether qompack eval should allow PASS for a confirmatory live run that has failed trials already counted under intention to treat, or stay inconclusive as it is now.

## Independent review

### review:eval2: needs-fixes

- **major** `internal/commands/cmd_eval_live.go:113-150 (notConfirmatory) and internal/eval/liveplan.go:111-135 (LivePreregistration)` — The code can call a run confirmatory when preregistration §9 says it is not. Its comment says it checks 'preregistration sections 3, 7 and 9', but it never checks §9's condition: 'The confirmatory run must be on a candidate where C1.12 and C1.1 are fixed; a run on a candidate with a known open defect is labelled with that defect and is not the confirmatory run.' The plan has no field for this and nothing labels the run. So a 40-trial run built from a bundle with C1.12 still open (PreCompact hookSpecificOutput rejected) prints 'confirmatory: yes', and LIVE-T01 is judged PASS or FAIL. The task asked for honest qualification, and the flag overclaims.
  - Evidence: grep for C1.12, C1.1, 'known open defect' and 'precondition' over internal/commands, internal/eval/liveplan.go and tools/devtool/liveeval.go returns nothing. notConfirmatory checks only the materials hashes, install path, model, arms, held-out, task count, trials per arm, skipped trials and a dirty bundle. preregistration.md §9 (lines ~150-160) makes the defect state part of what a confirmatory run is. The dry-run bundle from 109d675 carries no statement of which of C1.12 or C1.1 it fixes.
  - Fix: Add a recorded precondition to the plan, for example a required `--known-open-defects` flag on live-eval (value 'none' or a list), written to plan.json. Record the defects §9 requires fixed in LivePreregistration (RequiredFixed: ["C1.12","C1.1"]). In notConfirmatory, add a reason when the plan does not attest that they are fixed, or names any open defect. Also correct the comment, and pin both with a test (a plan with no attestation is not confirmatory). At minimum, renderLive and the JSON must state that the §9 defect precondition is not machine-checked, instead of printing an unqualified 'confirmatory: yes'.
- **minor** `internal/commands/cmd_eval_live.go:117-150 (notConfirmatory)` — notConfirmatory ignores foreign plugins, although §3 requires the two arms to be identical except for the plugin. SummarizeLive counts ArmSummary.ForeignPluginTrials and writes a note, but a run in which some other non-builtin plugin loaded on one arm is still reported 'confirmatory: yes' with judged gates. The arms then differ by more than Qompack.
  - Evidence: ForeignPluginTrials appears only in internal/eval/livetrial.go (summary note) and tools/devtool. grep over internal/commands finds no reference, so it never affects Confirmatory.
  - Fix: In notConfirmatory, add a reason for each arm whose ForeignPluginTrials > 0 (name the plugins from the summary note, or add a list to ArmSummary). Pin it with a commands test using a confirmatoryRun fixture that has ForeignPluginTrials=1.
- **minor** `internal/commands/cmd_eval_live.go:120` — The confirmatory check forbids the model contingency the pre-registration allows. §3 says that if the host rejects claude-sonnet-5, the run restarts with the alias `sonnet` after an append to §9. That run's plan.Model is 'sonnet' while plan.PreregisteredModel (from the hash-pinned tasks.json) stays 'claude-sonnet-5', so the pre-registered contingency run is always reported non-confirmatory.
  - Evidence: `if p.PreregisteredModel == "" || p.Model != p.PreregisteredModel` has no alias path. preregistration.md §3: 'the run is restarted with the host alias `sonnet` ... No other model change is permitted.'
  - Fix: Record the permitted contingency alias in LivePreregistration (ModelContingency: "sonnet"). Accept p.Model equal to that alias, with a note naming the resolved model the trials' init lines reported. Or state in preregistration §9 that invoking the contingency makes the run non-confirmatory. Either way, code and document must agree.
- **minor** `internal/commands/cmd_eval.go:248-258 (primaryPolicy)` — When a replay scores more than one Qompack policy, only the alphabetically first is judged. `test/replay --policies qompack-l3,qompack-rehydrate` judges qompack-l3, and qompack-rehydrate (the default product policy) is only named in a note. A failing qompack-rehydrate therefore cannot fail the evaluation when qompack-l3 passes, and the reverse also holds. This is the same arbitrary-choice defect that a81d63b fixed for null, narrowed but not removed.
  - Evidence: `if strings.HasPrefix(n, qompackPolicyPrefix) && (best == "" || n < best)`. test/replay/main.go:57 has defaultPolicies = "stock,null,oracle,qompack-rehydrate", and qompack-l3 is registered for --policies.
  - Fix: Prefer qompack-rehydrate (the default product policy) explicitly and fall back to the others by name, or judge every qompack-* policy and fail if any fails. Add a test with both policies scored and qompack-rehydrate failing.
- **minor** `internal/commands/evalartifacts.go:148-175 (newestLiveRun) and tools/devtool/liveeval.go:834-843 (writeJSONFile)` — One unreadable run under dist/live-eval makes every default `qompack eval` fail, including a run that is not the newest. writeJSONFile uses a plain os.WriteFile, so summary.json can be seen half-written: when eval runs while a run is finishing, or after a crash during the write. That run then fails decoding, and the command errors until someone deletes the directory. The newer-unfinished-run note (6a00064) only covers a summary that is missing, not one that is truncated.
  - Evidence: newestLiveRun calls `run, err := readLiveRun(sub); if err != nil { return nil, err }` for every subdirectory before sorting. writeJSONFile is `os.WriteFile(path, append(b, '\n'), liveFilePerm)`, with no temp file and rename.
  - Fix: Write plan.json and summary.json through a temp file plus rename in writeJSONFile. In newestLiveRun, sort by the plans first and read only the newest summary. If a non-newest run fails to decode, skip it with a note instead of failing the command, and keep failing closed when the newest run is unreadable.
- **nit** `tools/devtool/liveeval.go:553` — The code does not itself enforce A2's rule that 'a trial whose host reported no plugin list is a harness failure'. HostReportedPlugins=false only takes the trial out of the mismatch count. It is scored as a failure only if proc.Err happened to be set, which today follows from waitForResults failing. A stream with result lines but no init line (a host format change) would be neither a mismatch nor a harness failure, and on tool-output-recall or seed-recall the untouched fixture can then score a task success.
  - Evidence: `rec.HostReportedPlugins = stream.Init != nil` sets no HarnessError. summarizeArm keys its failures on `t.HarnessError != ""`.
  - Fix: In assemble, when `stream.Init == nil && rec.HarnessError == ""`, set rec.HarnessError = "the host reported no init line (no plugin state)", and extend TestSummarizeLive_NoPluginStateIsAHarnessFailureNotAMismatch to cover a trial with no init line and no proc error.
- **nit** `plans/sdd/V6-closeout/eval/preregistration.md A3; internal/eval/livetrial.go ToolUsesAfterSteps` — A3 exempts every mcp__plugin_qompack_qompack__* call on the grounds that such calls 'read no file'. But re_read and expand return archived file contents, so a re_read of cmd/probe/main.go is the same act as the Read of the program's source that A3 says still counts. Re-deriving the token also needs a program run, which is still caught, so this is not exploitable in practice. The rationale is still broader than it needs to be.
  - Evidence: internal/mcp/tools.go:95 says 're_read: read a file back as it was at a point in time, from the store's own' archive. A3 says 'a Read or Grep of the program's source' counts.
  - Fix: Exempt only the retrieval tools that return prior tool output or knowledge (recall, timeline, already_tried). Keep re_read and expand calls whose path argument matches the pattern counted, and amend the A3 wording to match.
- **nit** `commits 2af223c, a9c3cd3 (also 52dc908, f1e4cc5, 109d675, c2e6b1b)` — Two code-bearing commits have no Refs footer: 2af223c, a refactor of internal/eval and tools/devtool, and a9c3cd3, which adds a code comment and a new test. The branch's other code commits carry 'Refs: V6-VERIFY, C5.4'. No attribution trailers were found.
  - Evidence: `git log --format='%(trailers)' b070bbe..HEAD` is empty for 2af223c, 52dc908, a9c3cd3, f1e4cc5, 109d675 and c2e6b1b.
  - Fix: When the branch is next rewritten or integrated, add 'Refs: V6-VERIFY, C5.4' to 2af223c and a9c3cd3. Adding it to the docs-only commits is optional, since the base history also omits it on docs commits.

## Fix seat (review resolution) — status `done`, head `b7c3662fd666096479f8d26a4185bc2776a17dbc`

### Root cause

1. `notConfirmatory` checked materials, install, model, arms, held-out tasks, sizes and a dirty bundle. It had no input for the section 9 defect state, and it ignored `ArmSummary.ForeignPluginTrials`.
2. Model acceptance was an exact string match, and nothing recorded the model the host actually resolved.
3. `primaryPolicy` took `min(name)` over the qompack-* policies.
4. `writeJSONFile` used `os.WriteFile`, which rewrites the file in place, and `newestLiveRun` decoded every run's summary before choosing the newest.

### Summary

C5.4 fix seat, branch closeout/w2-eval2 in worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w2-eval2. I built on the implementer's 27 commits (b070bbe..c2e6b1b).

The resume check found nothing to handle: `git status` was clean when I started. The only files under runs/ were the implementer's committed evidence. There were no uncommitted edits and no runs/ files from the first, cut-off fix attempt, so I adopted nothing and discarded nothing.

All five review findings were real. Each has a failing test first, then a fix. The red logs are in plans/sdd/V6-closeout/w2-eval2/runs/fix/red-*.txt.

## Review resolution
1. **MAJOR: section 9 known-defect precondition. CONFIRMED and fixed (22f2a89).**
   - Evidence: `notConfirmatory` never read any defect state. TestEval_LiveNotConfirmatoryWithoutTheSection9DefectAttestation found an unattested 40-trial run confirmatory (runs/fix/red-f1-f2-f4-f5-commands.txt).
   - `devtool live-eval` now refuses to plan any run that has the qompack arm, dry runs included, unless `--known-open-defects` is given. The value is `none` or a list of checklist IDs; the ID shape is validated, and mixing `none` with IDs is refused.
   - plan.json records the statement as `known_defects {open, source}`. None is written as `[]`, so it is distinguishable from "not stated".
   - `eval.LivePreregistration.RequiredFixed = [C1.12, C1.1]`. A test pins it to the section 9 sentence.
   - `qompack eval` calls a run not confirmatory if the plan carries no statement or names any open defect, and the reason names the defect.
   - The JSON carries `known_defects` and `defect_precondition`, and the text prints a known-open-defects line. "confirmatory: yes" is now always followed by "its section 9 known-defect precondition rests on the operator's attestation, not on a machine check". summary.md states it too.
   - The comment on `notConfirmatory` is corrected.
   - Preregistration amendment A5 records this, including the section 9 command with `--known-open-defects none`.
2. **MINOR: foreign plugins. CONFIRMED and fixed (96c8e0f).**
   - ArmSummary gains `foreign_plugins` (the sorted names).
   - `notConfirmatory` adds a reason for each arm with `ForeignPluginTrials > 0`, naming the arm and the plugins.
   - Tests: TestEval_LiveNotConfirmatoryWhenAForeignPluginLoaded, plus an extended TestSummarizeLive_ForeignPluginsAreNamed.
3. **MINOR: model contingency. CONFIRMED and fixed by implementing it (e53ee1e).**
   - I chose to implement rather than declare the contingency non-confirmatory, because section 3 explicitly permits the `sonnet` restart.
   - LivePreregistration gains `Model = claude-sonnet-5` and `ModelContingency = sonnet`, pinned to section 3's text, with a `RunsPreregisteredModel` helper.
   - Each trial records `host_model` from its init line, and the summary records the distinct set as `host_models`.
   - devtool marks alias trials as on the pre-registered model only for a pre-registered task set.
   - `qompack eval` accepts an alias run only when every host reported the same one resolved model. It names that model and notes that section 3's own preconditions are not machine-checked: that the host rejected claude-sonnet-5 first, and that the change was appended to section 9 before the restart. An unrecorded resolution, a split resolution, or any other model is not confirmatory.
   - Amendment A6 records this. Test: TestEval_LiveTheSection3ModelContingencyCanBeConfirmatory.
4. **MINOR: primaryPolicy picked alphabetically. CONFIRMED and fixed (33911e8).**
   - Every scored qompack-* policy is now judged. qompack-rehydrate, the driver's default, leads with the plain IDs; each further policy's gates carry `@<policy>`, for example `TASK-01@qompack-l3`.
   - Red: with qompack-rehydrate failing, the old code returned no error.
   - Test: TestEval_ReplayJudgesEveryQompackPolicyItScored covers both failure directions and the all-pass case.
5. **MINOR: one unreadable run broke every default eval; writes were non-atomic. CONFIRMED and fixed (9f67dfa, 88e2a4a).**
   - `writeJSONFile` and summary.md now write a staging file in the same directory, sync it, chmod it and rename it over the target. Red: a hard-link test showed the old code rewrote the file in place (runs/fix/red-f5-writejson.txt).
   - `newestLiveRun` reads every finished run's plan to order the runs, then reads only the newest run's summary.
   - This differs from the reviewer's suggestion: older summaries are never read, so no "skipped" note is needed.
   - An unreadable newest summary still fails closed. An unreadable plan also fails closed, because without it the newest run cannot be determined.
   - Test: TestFileEvalArtifacts_OnlyTheNewestRunMustBeReadable.

The docs commit (b22a075) updates docs/user-guide.md with the confirmatory conditions, the policy suffix and the newest-run-only read.

## Dry run (task item 4, no model calls)
Evidence is in runs/fix/dry-run.
- The bundle was built from a clean tree at b22a075: `go run ./tools/devtool bundle --target windows/amd64 --version 0.3.0-eval2fix.dryrun --out dist/live-bundle-fix`. BUNDLE.json shows commit b22a075 and dirty=false.
- Without the new flag, the section 9 command refuses with exit 1.
- The A5 command with `--known-open-defects none --dry-run` plans 40 trials: task set 14e9ee33ccff, fixture tree 30cf769d2437, claude-sonnet-5, both arms, plugin-dir, with an absolute `--plugin-dir` path.
- The marketplace flow with an attested C1.11, the `--model sonnet` contingency, and the QOMPACK_LIVE_EVAL gate were each run: the gate refuses with exit 1 and creates no output directory.
- The bundled qompack.exe `eval --corpus` on the pilot1 run prints the new lines ("host reported not recorded", "known open defects … not attested") and exits 0.
- The bundle is left in the gitignored dist/live-bundle-fix.

### Commits

- 9f67dfa fix(devtool): write a live run's records whole, never in place
- 88e2a4a fix(commands): read only the newest live-eval run's summary
- 33911e8 fix(commands): judge every Qompack policy a replay scored
- 96c8e0f fix(commands): no confirmatory run when a foreign plugin loaded
- 22f2a89 fix(eval): confirm a live run only on an attested defect-free bundle
- e53ee1e fix(eval): let the section 3 model contingency run be confirmatory
- b22a075 docs(user-guide): describe what makes a live run confirmatory
- 04216a0 refactor(commands): wrap the contingency note's argument list
- eb10c32 docs(eval): record the fix seat's dry run and package runs
- b7c3662 docs(eval): record the fix seat's Linux non-root race run

### Tests

- `go test ./internal/commands -run "TestEval_LiveNotConfirmatoryWithoutTheSection9DefectAttestation|TestEval_LiveNotConfirmatoryWhenAForeignPluginLoaded|TestFileEvalArtifacts_OnlyTheNewestRunMustBeReadable|TestEval_ReplayJudgesEveryQompackPolicyItScored" -count=1 -v (before the fixes)` — RED as intended: all four FAIL, exit=1 (runs/fix/red-f1-f2-f4-f5-commands.txt)
- `go test ./tools/devtool -run "TestWriteJSONFile_ReplacesTheFileNeverRewritesIt" -count=1 -v (before the fix)` — RED as intended: FAIL, the file was rewritten in place (runs/fix/red-f5-writejson.txt)
- `go test ./tools/devtool -run "TestParseLiveFlags|TestRunLiveEval_TheQompackArmNeedsADefectAttestation" -count=1 and go test ./internal/commands -run "TestEval_LiveTheSection3ModelContingencyCanBeConfirmatory" -count=1 (before the fixes)` — RED as intended: build failed on the new fields (runs/fix/red-f1-devtool-attestation.txt, red-f3-model-contingency.txt)
- `go test ./internal/eval -count=1 -timeout=30m (Windows, b22a075)` — ok, exit=0
- `go test ./internal/commands -count=1 -timeout=30m (Windows, b22a075; re-run after the 04216a0 line wrap)` — ok, exit=0
- `go test ./internal/cli -count=1 -timeout=30m (Windows, b22a075)` — ok 22.9s, exit=0
- `go test ./tools/devtool -count=1 -timeout=30m (Windows, b22a075)` — ok 67.5s, exit=0
- `go test ./test/docs -count=1 -timeout=30m (Windows, b22a075)` — ok, exit=0
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w2-eval2 --out <scratch> HEAD fixseat-touched -- ./internal/eval ./internal/commands ./internal/cli ./tools/devtool ./test/docs (run at eb10c32, uid 10001, -race)` — all 5 PASS (eval 403/0/1 skip, commands 114, cli 349, devtool 299, docs 19); go_test_exit=0; stderr empty (runs/fix/linux)
- `go run ./tools/devtool gen-command-docs --check; gen-mcp-docs --check; gen-config-docs --check` — all up to date
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,runpatterns,docmarkers,coveragefloors ; go run ./tools/devtool fmt-check` — golangci-lint, nomagic, importgraph, testdeps, sleepcheck, docmarkers, coveragefloors and fmt-check pass. bindeps FAILs: golang.org/x/sys/unix reaches the linux/darwin binaries through internal/paths. That import comes from commit bbd8905 on the base; this branch does not change internal/paths, go.mod or go.sum. runpatterns FAILs on 26 -run patterns in other workstreams' wave-1 report.md files (config, e2e, hostperm, ingest, linux, packaging, rehydrate-cap); none are in eval or w2-eval2.
- `go vet ./internal/eval ./internal/commands ./tools/devtool ; go run ./tools/devtool fmt` — clean

### Criterion changes

- The confirmatory status of a live run is stricter: it now also requires the section 9 defect attestation with no open defect (A5), and no foreign plugin loaded on either arm (section 3). Rationale: the pre-registration already required both; the code did not enforce them.
- The confirmatory status is wider in one case: a run on the section 3 contingency alias `sonnet`, with one recorded resolved model, can now be confirmatory (A6). Rationale: section 3 explicitly permits that restart, and the old code could never produce the confirmatory run it describes.
- Replay gating is stricter: every scored qompack-* policy is judged, not just the alphabetically first. Further policies' gate IDs carry an @policy suffix.
- `devtool live-eval` with the qompack arm now requires --known-open-defects, dry runs included. The existing devtool tests got `defectsAttested: true` as input; the confirmatoryRun fixture now attests none. No assertion was deleted or loosened. I changed the text layout to 'model X (pre-registered Y), host reported Z' so the existing substring assertion still holds as written.
- Preregistration: amendments A5 and A6 are appended, dated 2026-09-25 and before any confirmatory trial. No task, fixture, hidden test, rate or analysis parameter changed; the section 2 hashes still match (TestLiveTaskSet_FrozenMaterialsMatchThePreregistration passes).

### Open issues

- Process incident: I ran `go run ./tools/devtool lint` without --only. Its stubskips sub-check runs a whole-tree `go test ./internal/... ./cmd/... ./test/...`, which breaks the no-./... rule. It ran from about 23:35 to 23:46 UTC, 2026-09-25. I stopped my own process tree (root bash 2164, whose command line carried this worktree's path). I also stopped two qompack.exe daemons orphaned by my killed e2e run (PIDs 58520 and 2120) and removed their build dir %TEMP%/qompack-e2e-1852714705. That dir was created at 23:35:29, before every other live e2e run started, which is how I attributed it. Any other workstream's wall-clock red in that window may be co-load from this run.
- bindeps lint FAIL on the integrated base: internal/paths imports golang.org/x/sys/unix (bbd8905), so it reaches the linux and darwin shipped binaries. Not caused by this branch.
- runpatterns lint FAIL: 26 malformed or unmatched -run patterns in the wave-1 report.md files of config, e2e, hostperm, ingest, linux, packaging and rehydrate-cap. Not eval.
- The confirmatory run must now pass `--known-open-defects none`, and the operator must actually know the bundle carries no open C1.1, C1.11 or C1.12. The ledger marks all three fixed, but the integrated gates are pending.
- Still not machine-checked, and reported as such: the section 9 defect attestation, and the section 3 contingency's own preconditions (the host rejected claude-sonnet-5 in the first session, and the section 9 append happened before the restart).

### Needs the owner

- Amendment A5 reads section 9's 'a run on a candidate with a known open defect ... is not the confirmatory run' as ANY known open defect, not only C1.12 and C1.1. That is the stricter reading, and it is consistent with both clauses. If the owner wants only C1.12 and C1.1 to disqualify a run, A5 must be amended before the first confirmatory trial.
- Amendment A6 lets a run on the `sonnet` contingency alias be confirmatory, which section 3 permits. It requires every trial's host to report one single resolved model. The owner should confirm that condition, or restate section 3, before the run.
- The coordinator should commit this summary as plans/sdd/V6-closeout/w2-eval2/report.md. Every -run pattern quoted here is a valid regexp that matches real tests, because runpatterns scans report files.


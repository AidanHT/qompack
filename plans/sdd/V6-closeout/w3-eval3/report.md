# Wave 3 workstream report: w3-eval3 (C5.5/D12)

Branch `closeout/w3-eval3`. Workflow `wf_eed51aa0-3c3`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `931536b0b783bda3a6cf87cf85450104c24dd11d`

### Root cause

(1) D12. In qompack-live-v1, tool-output-recall and seed-recall had `go vet ./...` as their only task check, and the untouched fixture passes it. A session that did nothing after the compaction therefore scored a task success, the primary outcome (H1). The gradeability test (TestLiveTaskSet_ReferenceSolutionsPassEveryCheck) only required the untouched fixture to fail SOME check, and a recovery check satisfied that, so the weakness went unseen. regression-guard's constraint check `pinned` ran `go test ./list -run ^TestHiddenEvalParseListPinned$`, which compiles every test file in list/, including the hidden JoinList test. A trial that never wrote JoinList failed `pinned` on `undefined: list.JoinList` although it never touched ParseList, so one omission counted in both H1 and H2. Evidence: runs/red-01-v1-task-checks.txt. (2) The live summary's decision already applies section 8's intention-to-treat rule (a harness failure is scored as a failure on every outcome). qompack eval's verdictOf then overrode that decision with its own `inconclusive` whenever any live trial failed (runs/red-03-commands-itt-superseded.txt). (3) Two gaps found while verifying. First, a run of the superseded v1 set would have been planned by live-eval (red-02) and called confirmatory by qompack eval (red-03). Second, the dry run printed neither the bundle's commit and dirty flag nor any check against the pre-registration, so a departure (a dirty bundle, a missing --include-held-out, a changed trial count) would surface only after the 40 sessions were spent (red-04).

### Summary

Workstream w3-eval3: branch closeout/w3-eval3 in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w3-eval3, base closeout/integration 54a4334, HEAD 931536b. The tree is clean. Nothing was pushed, and no real Claude session was started. The three held-out tasks were graded offline only, as the existing reference test always did; no session ran them.

## (1) D12: the task set is fixed and frozen as qompack-live-v2, before any trial
- **What v2 is.** testdata/eval/live/tasks-v2.json (id qompack-live-v2) is qompack-live-v1 with exactly these changes, and TestLiveTaskSetV2_DiffersFromV1OnlyAsAmendmentA7States pins that:
  - **tool-output-recall** gains hidden-v2/tool-output-recall/check and a task check `behaviour` (`go test ./...`). The hidden test passes only when TOKEN holds 975408daf9b5 and nothing else but whitespace.
  - **seed-recall** (held out) gains hidden-v2/seed-recall/config and a task check `behaviour`. The hidden test compiles only when config.Seed is a string constant (`const seed string = config.Seed`), and passes only when it equals 5cc87334711f2d40.
  - **regression-guard** takes its hidden tests from hidden-v2/regression-guard. The JoinList test stays in list/. The pinned ParseList test, with the same cases, moves alone into package pinned/. `pinned` now runs `go test ./pinned -run ^TestHiddenEvalParseListPinned$`.
  - The id and the description.
- **Unchanged:** every prompt and fixture, every other check (`builds` and `vets` stay), the defaults, the analysis (claude-sonnet-5, 95%, margin 0.20, 2 trials per arm), the section 4 table, the rates and the 40-session sample size.
- **New hashes:** task set d59dc09d15edb04acb567dd97ac620c41eaf00206093bdc59a5162607619af83. Fixture tree a5f57b1e2045378d1b38a629f75f830755e95e3582513ea8e3d3fb0d452ec3d2 (A1's recipe over fixtures, hidden and hidden-v2). Both were derived two ways: `sha256sum`, and `find fixtures hidden hidden-v2 -type f | LC_ALL=C sort | xargs sha256sum --text | sha256sum` compared with eval.TreeManifestSHA256.
- **v1 is kept as the record.** Its files are byte-identical: tasks.json still hashes to 14e9ee33, and the fixtures+hidden tree to 30cf769d. It is recorded in eval.LivePreregistrations with SupersededBy qompack-live-v2 and the reason. devtool live-eval refuses to plan it, dry run included, and qompack eval never calls a run of it confirmatory.
- **Amendment A7** is appended to plans/sdd/V6-closeout/eval/preregistration.md, dated 2026-09-25, before any trial of either set. It carries the reason, the exact change, the frozen-materials table, v1's superseded status, and the new section 9 command. It also notes the qompack eval intention-to-treat change as a code-to-document correction, not an amendment.
- **Tests (tools/devtool):**
  - TestLiveTaskSet_ReferenceSolutionsPassEveryCheck is still green, now over v2, the pilot and v1.
  - New TestLiveTaskSet_AnUntouchedFixtureFailsTheTaskOutcome: for every v2 and pilot task, the untouched fixture fails a *task* check. This is stricter than the old any-check rule, which v1 met only through recovery checks.
  - New TestLiveTaskSet_RegressionGuardPinnedGradesOnlyParseList: `pinned` passes without JoinList while `join` and `all` fail, and a ParseList that stops trimming tabs fails `pinned`.
  - New TestLiveTaskSet_ToolResultTasksGradeTheDeliverable: a wrong token, a token inside a sentence, a `var Seed` and a placeholder all fail the task check.
- **Tests (internal/eval):** TestLiveTaskSet_FrozenMaterialsMatchThePreregistration and TestLivePreregistrations_MatchTheDocumentAndTheMaterials now read and pin both sets' hashes out of the document (section 2 and A1 for v1, A7 for v2). The fixture-tree rule moved to eval.LiveTaskSet.FixtureTreeDirs.

## (2) qompack eval reports the intention-to-treat decision (coordinator default)
- A live run's failed trials no longer force `inconclusive`. The verdict follows the pre-registered decision, which already counts every failed trial in every denominator.
- What still forces `inconclusive`: planned trials that never ran (and A4 still makes a stopped run not-applicable), and a replay's failed trials, because replay scores do not count them.
- The report now:
  - adds `failed_treatment`: a harness failure counts as a failure on every outcome, any other failed trial is graded by its checks, and a plugin-state mismatch makes the decision not-applicable;
  - prints `failed trials: N, …` before each `failed trial: <name>`;
  - appends `N failed trial(s) counted under intention to treat` to LIVE-T01's detail.
- docs/user-guide.md is updated by hand. The generated pages carry no verdict text, and gen-command-docs, gen-config-docs and gen-mcp-docs `--check` are all up to date.

## (3) Verifying the w2-eval2 fix seat's work, plus one addition
- **The addition.** eval.LivePlanDepartures now holds the plan-time half of the confirmatory rule, moved verbatim out of qompack eval's notConfirmatory; the commands tests pass unchanged.
  - devtool live-eval prints the bundle's version, commit, dirty flag and BUNDLE.json sha256, then either "confirmatory preconditions at plan time: met" or every departure by name.
  - A new `--confirmatory` flag refuses to plan, dry run included, when there is any departure.
- **Bundle:** built from b891638 on a clean tree with `go run ./tools/devtool bundle --target windows/amd64 --version 0.3.0-eval3.dryrun --out dist/live-bundle-eval3` (dirty=false, BUNDLE.json sha256 0d4018b2…).
- **Dry runs** (QOMPACK_LIVE_EVAL unset, every invocation under `env -u`), evidence in runs/dry-run/:
  - The task's command, with and without `--confirmatory`, exits 0 and plans 40 trials of qompack-live-v2 (d59dc09d15ed, fixture tree a5f57b1e2045).
    - Model: `--model claude-sonnet-5` on both arms' host lines.
    - Arms: stock and qompack, each arm first 10 times.
    - Trial order: every task has 4 trials, in pairs, held-out tasks included (12 lines).
    - Plugin: an absolute --plugin-dir.
    - Plan-time preconditions: met.
  - Refused as intended:
    - v1;
    - `--confirmatory` without `--include-held-out` ("excluded the held-out tasks; planned 7 of the task set's 10 tasks");
    - `--confirmatory` with `--known-open-defects C1.13`;
    - the real run without QOMPACK_LIVE_EVAL=1, which made no directory.
  - The default `--tasks` is now v2.
  - The bundled qompack.exe `eval` on pilot1 reports "not confirmatory" with its reasons and writes nothing.

## Command for the confirmatory trial
Run it from a clean checkout of the frozen candidate (C3.1). The bundle records its commit and must show dirty=false. First run the same live-eval command with `--dry-run` and confirm "preconditions at plan time: met".
```
go run ./tools/devtool bundle --target windows/amd64 --version 0.3.0 --out dist/live-bundle
QOMPACK_LIVE_EVAL=1 go run ./tools/devtool live-eval --tasks testdata/eval/live/tasks-v2.json --include-held-out --arms stock,qompack --install plugin-dir --known-open-defects none --bundle dist/live-bundle/qompack-plugin-0.3.0-windows-amd64 --max-sessions 40 --confirmatory
```
Only pass `--known-open-defects none` if the ledger shows no open known defect on that candidate (A5). If any are open, list their IDs; `--confirmatory` will then refuse, and a run without it is not confirmatory. Afterwards: `qompack eval --corpus dist/live-eval/<run-id>`.

## Machine load
Three other workstreams were running tests on this machine at the same time. No wall-clock failure occurred, so nothing needed a solo re-run. One focused devtool run took 142 s against a usual ~35 s, which is load, not a failure.

### Commits

- 79d079b feat(eval): freeze task set qompack-live-v2 before any trial
- 04c1556 fix(devtool): refuse to plan a superseded live task set
- 7238232 fix(commands): never confirm a run of a superseded task set
- 6aeb220 fix(commands): report the intention-to-treat live decision
- 30e4dd0 docs(user-guide): describe the v2 task set and the live verdict
- 20a2d2d docs(eval): record the w3-eval3 red runs and package runs
- 061bcb4 refactor(eval): share the plan-time confirmatory check
- 0e9e1fa feat(devtool): say at plan time whether a live run can confirm
- 6fd0fdc docs(user-guide): note the live-eval plan-time confirmatory check
- b891638 docs(eval): record the w3-eval3 runs at 6fd0fdc
- 931536b docs(eval): record the qompack-live-v2 dry run

### Tests

- `go test ./tools/devtool -run '^TestLiveTaskSet_AnUntouchedFixtureFailsTheTaskOutcome$' -count=1 -v, then -run '^TestLiveTaskSet_RegressionGuardPinnedGradesOnlyParseList$', against qompack-live-v1 (before v2 existed)` — RED as intended, exit 1: seed-recall passes every task check ([vets]); tool-output-recall passes ([builds]); pinned fails with 'undefined: list.JoinList'. Log: plans/sdd/V6-closeout/w3-eval3/runs/red-01-v1-task-checks.txt
- `go test ./tools/devtool -run '^TestRunLiveEval_ASupersededTaskSetIsNeverPlanned$' -count=1 -v (guard and default temporarily reverted)` — RED as intended, exit 1: 'An error is expected but got nil' (runs/red-02-superseded-set-planned.txt); green with the guard
- `go test ./internal/commands -run '^TestEval_LiveFailedTrialsAreCountedInTheDecisionAndListed$' -count=1 -v, then -run '^TestEval_LiveNotConfirmatoryOnASupersededTaskSet$'` — RED as intended before the fixes, exit 1: verdict 'inconclusive' where 'pass' was expected, and the v1 run was confirmatory (runs/red-03-commands-itt-superseded.txt); green after
- `go test ./tools/devtool -run '^TestRunLiveEval_ThePlanSaysWhetherItIsTheConfirmatoryDesign$' -count=1 -v` — RED as intended before 0e9e1fa: the dry run did not contain the bundle line (runs/red-04-plan-time-preconditions.txt); green after
- `go test ./tools/devtool -run '^TestLiveTaskSet_' -count=1` — ok (reference solutions for v2, pilot and v1; untouched-fixture task rule; pinned; tool-result deliverables)
- `go test ./internal/eval -run '^TestLivePlanDepartures_' -count=1` — ok
- `go test ./internal/eval -count=1 -timeout=30m (Windows, at 30e4dd0 and at 6fd0fdc)` — ok, exit 0 (runs/tests/internal-eval-windows-6fd0fdc.txt)
- `go test ./internal/commands -count=1 -timeout=30m (Windows, at 30e4dd0 and at 6fd0fdc)` — ok, exit 0
- `go test ./internal/cli -count=1 -timeout=30m (Windows, at 30e4dd0 and at 6fd0fdc)` — ok, exit 0
- `go test ./tools/devtool -count=1 -timeout=30m (Windows, at 30e4dd0 and at 6fd0fdc)` — ok, exit 0
- `go test ./test/docs -count=1 -timeout=30m (Windows, at 30e4dd0 and at 6fd0fdc)` — ok, exit 0
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w3-eval3 --out plans/sdd/V6-closeout/w3-eval3/runs/linux 6fd0fdc eval3-touched --timeout 30m -- ./internal/eval ./internal/commands ./internal/cli ./tools/devtool ./test/docs` — PASS, uid 10001, -race, GOMAXPROCS=4: eval 407/0 with 1 pre-existing skip, commands 115, cli 371, devtool 333, docs 19; go_test_exit=0; stderr empty; source unchanged. The same gate at 30e4dd0 also passed.
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/eval/... ./internal/commands/... ./tools/devtool/... (at 6fd0fdc)` — exit 0, no findings
- `go run ./tools/devtool fmt-check; go vet ./internal/eval ./internal/commands ./tools/devtool` — clean at every commit
- `go run ./tools/devtool gen-command-docs --check; gen-config-docs --check; gen-mcp-docs --check` — all up to date, exit 0
- `go run ./tools/devtool lint --only=nomagic,importgraph,sleepcheck,runpatterns,docmarkers` — exit 1. nomagic, importgraph, sleepcheck and docmarkers PASS. runpatterns FAILs on 16 pre-existing patterns, all in the w2-lifetime, w2-lint and w2-sessionend report.md files; none are in this branch's files.
- `go run ./tools/devtool bundle --target windows/amd64 --version 0.3.0-eval3.dryrun --out dist/live-bundle-eval3 (clean tree at b891638)` — exit 0, 11 files, dirty=false, BUNDLE.json sha256 0d4018b2ac0e77f39bc72c9cda802620d41bf12367596cb1aa38cb7b4232418c
- `env -u QOMPACK_LIVE_EVAL go run ./tools/devtool live-eval --tasks testdata/eval/live/tasks-v2.json --include-held-out --arms stock,qompack --install plugin-dir --bundle dist/live-bundle-eval3/qompack-plugin-0.3.0-eval3.dryrun-windows-amd64 --known-open-defects none --max-sessions 40 --confirmatory --dry-run` — exit 0: 40 trials, task set d59dc09d15ed, fixture tree a5f57b1e2045, claude-sonnet-5, both arms balanced 10/10 first, plugin-dir absolute, preconditions met, no session started (runs/dry-run/02-*.txt; the task's literal command without --confirmatory is 03-*.txt)
- `the same without --dry-run and without QOMPACK_LIVE_EVAL, --claude C:/nonexistent/claude.exe --out <scratch>` — refused, exit 1, no output directory created (runs/dry-run/08-*.txt)

### Criterion changes

- The confirmatory task set moves from qompack-live-v1 to qompack-live-v2 (D12, amendment A7). Two task checks are added (stricter); `pinned` compiles only the pinned test. Rationale: an untouched fixture passed both tasks' only task check, and `pinned` failed trials that never touched ParseList. `pinned` still fails on a changed ParseList, or on a list package that does not build, because the teams relying on ParseList could not build against it either.
- The untouched-fixture rule is stricter for v2 and the pilot: the untouched fixture must fail a TASK check, not just any check. v1 stays graded under its original any-check rule, as the record of the set A7 superseded.
- qompack eval verdict (coordinator default of 2026-09-25): failed live trials no longer force `inconclusive`; the verdict is the pre-registered decision. TestEval_LiveFailedTrialsKeepTheVerdictInconclusive is replaced by TestEval_LiveFailedTrialsAreCountedInTheDecisionAndListed. The new test asserts the failed trial costs its arm a task success, the verdict follows the decision (pass when non-inferior, inconclusive when four failures leave the interval straddling the margin) and each failed trial is named. Side effect: a NON-confirmatory live run with failed trials, read beside a passing replay, now reads PASS from the replay, as the same live run without failed trials already did. Skipped live trials and failed replay trials still force inconclusive.
- Stricter: a run of a superseded task set is never confirmatory, and live-eval refuses to plan one, dry run included.
- Additive: live-eval prints the plan-time confirmatory preconditions and the bundle identity; the new --confirmatory flag refuses to plan on any departure. The default --tasks is now tasks-v2.json. qompack eval's notConfirmatory now takes its plan-level reasons from eval.LivePlanDepartures, with the same texts; only their order moved (bundle reasons now come before skipped trials and foreign plugins).

### Open issues

- Not changed, because it would loosen checks and needs an owner decision plus another amendment and task-set id before any trial. Constraint checks on a file the trial never wrote fail closed, per the grader's rule that an ungraded check is never a pass. A trial that did no work therefore also records constraint violations in: constraint-verbatim (doc-lerp, doc-sign, stdlib-lerp, stdlib-sign), changing-requirement-format (no-sprint), decision-rationale (no-gob-load), constraint-naming (spec-name, no-other-names, parallel) and changing-requirement-slug (no-regexp). It is the same double count as `pinned`, applies equally to both arms, and is pre-registered behaviour.
- seed-recall's recovery regex (unchanged from v1) requires the untyped form `const Seed = "..."`, which the prompt dictates. A typed constant holding the right seed passes the new task check but fails recovery.
- Section 9 precondition: the ledger still shows C1.13, C1.15–C1.20 and the D11/D13 wave-3 follow-ups unticked. Under A5 any known open defect disqualifies the run, so the confirmatory run should wait for a frozen candidate (C3.1) whose ledger has none open.
- Commit 6fd0fdc's subject is 65 characters, one over the 64 limit. It was not rewritten, because committed evidence names its SHA and b891638's (the test logs, the Linux run and the bundle's recorded commit). HEAD's over-long subject was amended (931536b) before anything referred to it.
- Carried from w2-eval2: section 8's infrastructure re-run and sensitivity policy is not implemented in the driver; a re-run is a separate run by hand.
- Pre-existing: runpatterns reports 16 unsatisfiable patterns in the w2-lifetime, w2-lint and w2-sessionend reports. When this report is committed as .md, its -run patterns are single valid test names with no '|'.
- Left in place: the dry-run bundle in the gitignored dist/live-bundle-eval3 (it is not the candidate) and the Linux /work/cx-w3-eval3-* directories.

### Needs the owner

- Confirm or overrule the coordinator default that qompack eval reports the intention-to-treat decision rather than forcing inconclusive when trials failed. To overrule, revert 6aeb220 and the verdict paragraph of 30e4dd0; the task-set work is independent of it.
- Acknowledge amendment A7: qompack-live-v2 replaces qompack-live-v1 before use, with two added task checks and `pinned` compiled apart from the JoinList test.
- Decide whether constraint checks on files a trial never wrote should keep failing closed (a trial that did no work also counts constraint violations in five tasks). Changing it needs a new task-set id and amendment before any trial.
- Before the confirmatory run, state which known defects the frozen candidate still carries. `--known-open-defects none` is the operator's attestation, and A5 disqualifies any open known defect.

## Independent review

### review:eval3: needs-fixes

- **major** `internal/commands/cmd_eval.go:248-251 (verdictOf), with internal/commands/cmd_eval_live.go:237-246 (liveGates)` — Removing `rep.Live.Trials.Failed > 0` from verdictOf lets a confirmatory live run whose pre-registered decision is `not-applicable` read as an overall PASS when a passing replay is read beside it. A plugin-state mismatch always puts the trial in s.Failed, so at base the failed-trial rule forced `inconclusive` here. Now LIVE-T01 is left nil ('the pre-registered rule reached no verdict'), the replay gates supply `decided > 0`, and the verdict falls through to pass. Plain `qompack eval` with no --corpus reads both the newest dist/live-eval run and testdata/bench-replay.json (evalartifacts.go:61-89), so this case is reachable. It contradicts the coordinator default being implemented ('report the pre-registered intention-to-treat decision'). It also contradicts the report's own failed_treatment text ('a plugin-state mismatch makes the decision not-applicable'). The implementer disclosed the side effect only for NON-confirmatory runs. The same fall-through now also covers a confirmatory run whose failures push the ITT decision to `inconclusive`.
  - Evidence: Scratch probe in an archived copy of HEAD (package commands_test, reusing confirmatoryRun/liveTrials/inputWith/goodScore/ranTrials): one qompack trial with PluginLoaded=false. Live only: decision=not-applicable, confirmatory=true, failed=1, verdict=inconclusive. With a passing replay: decision=not-applicable, confirmatory=true, failed=1, verdict=pass. At base 54a4334 the `rep.Live.Trials.Failed > 0` clause returned inconclusive for both.
  - Fix: In verdictOf, add a case before the default so a confirmatory live run withholds the pass when its pre-registered decision withholds one. At minimum: `case rep.Live != nil && rep.Live.Confirmatory && rep.Live.Decision.Verdict == "not-applicable": return VerdictInconclusive`. Better: return inconclusive whenever a confirmatory run's LIVE-T01 is nil, so a live inconclusive decision is not overridden by a replay either. That is an owner-visible policy choice, so state it in the commit. Add a test next to TestEval_ReplayAndLiveAreReportedTogether: a confirmatory run with one plugin-mismatch trial plus a passing replay must not read `pass`. Mention the case in docs/user-guide.md's verdict paragraph.
- **nit** `plans/sdd/V6-closeout/eval/preregistration.md (amendment A7, section 9 command block)` — A7's replacement section 9 command omits `--confirmatory`, and has no instruction to dry-run first. The implementer's recommended confirmatory command includes both. A7 was written before the flag existed (79d079b, before 0e9e1fa). The command the pre-registration records is therefore the less safe one, which plans and starts a run that departs from the design.
  - Evidence: A7 block: `QOMPACK_LIVE_EVAL=1 go run ./tools/devtool live-eval --tasks testdata/eval/live/tasks-v2.json --include-held-out --arms stock,qompack --install plugin-dir --known-open-defects none --bundle ... --max-sessions 40` (no --confirmatory). Report's recommended command: `... --max-sessions 40 --confirmatory`.
  - Fix: No trial exists yet, so append `--confirmatory` to A7's command and add one sentence: run it with `--dry-run` first and confirm 'confirmatory preconditions at plan time: met'. Keep TestLivePreregistrations_MatchTheDocumentAndTheMaterials' A7 regex matching (it anchors on `--tasks <file> ... --known-open-defects none`).
- **nit** `commit 6fd0fdc; commits 20a2d2d, b891638, 931536b` — Commit hygiene. 6fd0fdc's subject is 65 characters, over the 64 limit (disclosed; not rewritten because evidence names its SHA). The three docs(eval) evidence commits carry no `Refs:` footer, while every code commit on the branch has `Refs: V6-VERIFY, C5.5/D12`. The base has precedent for evidence commits without a footer (e.g. 4aaffea). No attribution trailers were found, and the commits are unsigned as the user directed.
  - Evidence: git log --format='%(trailers:only)': 20a2d2d, b891638 and 931536b show none; `git log -1 --format=%s 6fd0fdc | wc -c` gives 66 (65 characters plus the newline).
  - Fix: No rewrite is needed, because SHAs are cited in the evidence. Add `Refs: V6-VERIFY, C5.5/D12` to future evidence commits in this workstream, and record the over-long subject in the workstream report.

## Fix seat (review resolution) — status `done`, head `a1b5614`

### Root cause

Commit 6aeb220 removed `rep.Live.Trials.Failed > 0` from verdictOf (internal/commands/cmd_eval.go). That clause had been the only thing that stopped a confirmatory live run with an undecided pre-registered rule from falling through to `pass` when a replay was read beside it. A plugin-state mismatch always makes eval.DecideLive return `not-applicable` and always puts the trial in Summary.Failed. liveGates then leaves LIVE-T01 nil ("the rule reached no verdict"), and the passing replay gates supply `decided > 0`, so the verdict came out `pass`. Plain `qompack eval` reads both dist/live-eval and testdata/bench-replay.json, so a real run can reach this. The same fall-through also let a replay pass a confirmatory run whose interval straddles the margin (`inconclusive`). For a run with zero failed trials that case already existed at base 54a4334: the base switch only checked Failed/Skipped/Ran, so Failed=0 fell through to the replay's decided gates. I established that by reading the code, not by running base. Red evidence: runs/red-05-replay-passes-undecided-confirmatory.txt. The new test failed at 931536b with only the test added, and across -count=12 its first failing case was 11x "plugin mismatch (replay read: true)" and 1x "interval straddles (replay read: true)".

### Summary

## The review finding was correct and is fixed

When the pre-registered rule of a confirmatory live run reaches no verdict, `qompack eval` now reports `inconclusive`, even when a passing replay is read beside it. "No verdict" means the decision was `inconclusive` or `not-applicable`. A failing gate still fails the run, including a constraint regression (LIVE-T02).

## Review resolution
- **Finding (major)**: removing the failed-trial clause from `verdictOf` let a confirmatory run with no pre-registered decision read `pass` next to a passing replay.
  - **Verdict:** confirmed.
  - **Action:** fixed, using the broader of the reviewer's two options. The verdict is `inconclusive` whenever a confirmatory run's LIVE-T01 is undecided, not only when the decision is `not-applicable`.
    - I picked the broader option because the coordinator default and the docs both say the verdict *is* the pre-registered decision (intention to treat).
    - The minimal option would still let a replay override a confirmatory `inconclusive`, which already happened at base for runs with no failed trials.
  - **Changes:**
    - `verdictOf` has a new case: `rep.Live != nil && rep.Live.Confirmatory && !liveDecisionReached(rep.Live.Decision)` returns `VerdictInconclusive`.
    - New helper `liveDecisionReached` in `internal/commands/cmd_eval_live.go`. It returns true only for superior, non-inferior or inferior. `liveGates` now uses it too, so the gate and the verdict cannot read the decision differently.
    - The `VerdictInconclusive` and `verdictOf` doc comments are updated.
  - **Test:** `TestEval_AReplayCannotPassAConfirmatoryRunThatReachedNoDecision` in `internal/commands/eval_live_test.go`, next to `TestEval_ReplayAndLiveAreReportedTogether`. It covers three cases, each run live-only and with a passing replay:
    - a plugin mismatch, expected `inconclusive`;
    - an interval straddling the margin, expected `inconclusive`;
    - a mismatch plus a constraint regression, expected `fail`.
    - For every case it also asserts `Confirmatory == true` and that LIVE-T01 is nil.
  - **Docs:** the verdict paragraph in `docs/user-guide.md` now describes this case. I also added one sentence to amendment A7's closing note on reading the result in `plans/sdd/V6-closeout/eval/preregistration.md`, before any trial. It changes no task, fixture, hidden test, rate or analysis parameter, and `TestLiveTaskSet_FrozenMaterialsMatchThePreregistration` still passes.

## Criterion change (rationale)
This makes the check stricter; nothing is weakened. It is also a policy choice the owner can see and may overrule, and the commit body says so.

A confirmatory live run is the pre-registered evaluation. A deterministic, model-free replay cannot stand in for a pre-registered verdict that the live rule did not reach. One consequence is new behaviour: a confirmatory run with **zero** failed trials whose decision is `inconclusive` used to read `pass` next to a passing replay, and now reads `inconclusive`.

Runs that are not confirmatory are unchanged, as the implementer disclosed. Their gates are not judged, so a replay beside them still decides.

## Dry run of the qompack-live-v2 plan at b225fcd (no model called, QOMPACK_LIVE_EVAL unset)
- **Bundle:** built from b225fcd on a clean tree (`dirty=false`); BUNDLE.json sha256 `a6193945a231171bede24bb108e02f1e632b080a109695fad019395aa7a72e8c`.
- **Command:** `env -u QOMPACK_LIVE_EVAL go run ./tools/devtool live-eval --tasks testdata/eval/live/tasks-v2.json --include-held-out --arms stock,qompack --install plugin-dir --bundle dist/live-bundle-eval3-fix/qompack-plugin-0.3.0-eval3.fixdryrun-windows-amd64 --known-open-defects none --max-sessions 40 --confirmatory --dry-run` exited 0.
- **What the plan resolved to:**
  - 40 trials of task set `qompack-live-v2`: sha `d59dc09d15ed…`, fixture tree `a5f57b1e2045…`, both matching amendment A7's table.
  - Model `claude-sonnet-5`, arms stock and qompack, install `plugin-dir` with an absolute `--plugin-dir`.
  - 10 tasks including the 3 held-out ones, 20 trials per arm; each arm goes first in 10 of the pairs.
  - The trial order is identical to the implementer's dry run at 931536b.
  - It printed "confirmatory preconditions at plan time: met".
- **Dirty-bundle refusal (useful evidence):** my first bundle build wrote its log into the worktree during the build, so it read `dirty=true` and `--confirmatory` refused it. That log is `runs/dry-run-fix/00-dirty-bundle-refused.txt`.
- **Bundled binary:** `qompack.exe eval` on pilot1 prints what the 931536b record shows.
- **Held-out tasks:** not run by anyone. No real Claude session was used.

## Command the coordinator should run for the confirmatory trial
Run it from the final integrated commit, with a clean tree, and with nothing written into the tree during the bundle build:
```
go run ./tools/devtool bundle --target windows/amd64 --version <v> --out dist/live-bundle
QOMPACK_LIVE_EVAL=1 go run ./tools/devtool live-eval --tasks testdata/eval/live/tasks-v2.json \
  --include-held-out --arms stock,qompack --install plugin-dir --known-open-defects none \
  --bundle dist/live-bundle/qompack-plugin-<v>-windows-amd64 --max-sessions 40 --confirmatory
```
Then read the result with `<bundle>/bin/qompack.exe eval --corpus dist/live-eval/<run-id>`. With `--corpus` set to the run directory only the live run is read, so the verdict is exactly the pre-registered decision.

## Evidence files
All under `C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w3-eval3/plans/sdd/V6-closeout/w3-eval3/runs/`:
- `red-05-replay-passes-undecided-confirmatory.txt`
- `tests/green-05-replay-cannot-pass-undecided-0a7b320.txt`
- `tests/internal-commands-windows-0a7b320.txt`
- `tests/test-docs-windows-0a7b320.txt`
- `tests/golangci-lint-touched-0a7b320.txt`
- `tests/fmt-vet-gen-0a7b320.txt`
- `linux/gate-host-output-0a7b320.txt` and `linux/cx-w3-eval3-fix-touched-0a7b320-0a7b320-20260926T024134Z-artifacts/`
- `dry-run-fix/`

Commit 0a7b320 was renamed to 7aa4e03 by a subject-only amend; both have the same tree, b6960dff.

## Process note
I ran `go run ./tools/devtool lint --only=nomagic,sleepcheck,stubskips,runpatterns,docmarkers`. I did not realise that stubskips runs a whole-tree `go test -json ./internal/... ./cmd/... ./test/...`, which breaks the no-whole-tree rule. It ran for about 25 minutes on the loaded machine before I stopped it.
- I stopped only my own tree, found by parent-PID lineage from the devtool process whose command line carried my exact `--only` list: PIDs 23816, 2308, 29824, 31712, 46336, 58160, 47640, 44860.
- Afterwards I checked for leftovers: no qompack or test process of mine remained. The guards, fault and qompack.exe processes still running belong to other workstreams, and their parents are alive.
- The nomagic and sleepcheck subchecks had already passed. I did not re-run runpatterns or docmarkers: no -run pattern in any .md file changed, and at base runpatterns already fails on other workstreams' reports, per the implementer's `generators-and-lint-windows.txt`.

The windows/amd64 dry-run bundle is left in `dist/live-bundle-eval3-fix/`, which git ignores.

### Commits

- a570ea8 fix(commands): no replay can pass an undecided confirmatory run
- 7aa4e03 docs(eval): a replay cannot pass an undecided confirmatory run
- b225fcd docs(eval): record the w3-eval3 fix seat's runs
- a1b5614 docs(eval): record the qompack-live-v2 dry run at b225fcd

### Tests

- `go test ./internal/commands -run 'TestEval_AReplayCannotPassAConfirmatoryRunThatReachedNoDecision' -count=1 (at 931536b + test only)` — FAIL as expected (exit 1): plugin mismatch with replay read gave pass, want inconclusive
- `go test ./internal/commands -run 'TestEval_AReplayCannotPassAConfirmatoryRunThatReachedNoDecision' -count=12 (at 931536b + test only)` — FAIL (exit 1); first failing case per iteration: 11x plugin mismatch, 1x interval straddles (both with a replay read)
- `go test ./internal/commands -run 'TestEval_AReplayCannotPassAConfirmatoryRunThatReachedNoDecision' -count=12 -v (at 0a7b320)` — PASS 12/12, exit 0
- `go test ./internal/commands -count=1 (Windows, at 0a7b320)` — ok, exit 0
- `go test ./test/docs -count=1 (Windows, at 0a7b320)` — ok, exit 0
- `go test ./internal/eval -run 'TestLiveTaskSet_FrozenMaterialsMatchThePreregistration' -count=1 (after the preregistration edit)` — PASS, exit 0
- `go test ./tools/devtool -run 'TestLiveTaskSet_ReferenceSolutionsPassEveryCheck' -count=1` — ok (18.9s), exit 0
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w3-eval3 --out plans/sdd/V6-closeout/w3-eval3/runs/linux 0a7b320 fix-touched-0a7b320 --timeout 30m -- ./internal/commands ./internal/cli ./test/docs` — Linux non-root uid 10001 with -race: internal/commands pass=116 fail=0, internal/cli pass=371 fail=0, test/docs pass=19 fail=0; go_test_exit=0; the new test passed there
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/commands/...` — exit 0, no findings
- `go run ./tools/devtool fmt-check; go vet ./internal/commands; go run ./tools/devtool gen-command-docs --check` — all exit 0; docs/commands.md up to date
- `go run ./tools/devtool bundle --target windows/amd64 --version 0.3.0-eval3.fixdryrun --out dist/live-bundle-eval3-fix (clean tree at b225fcd)` — exit 0, dirty=false, BUNDLE.json sha256 a6193945…
- `env -u QOMPACK_LIVE_EVAL go run ./tools/devtool live-eval --tasks testdata/eval/live/tasks-v2.json --include-held-out --arms stock,qompack --install plugin-dir --bundle dist/live-bundle-eval3-fix/qompack-plugin-0.3.0-eval3.fixdryrun-windows-amd64 --known-open-defects none --max-sessions 40 --confirmatory --dry-run` — exit 0: 40 trials, qompack-live-v2 d59dc09d15ed / fixture tree a5f57b1e2045, claude-sonnet-5, 10 tasks incl. held-out, 20 per arm, each arm first in 10 pairs, plan-time preconditions met, no session started
- `<bundle>/bin/qompack.exe eval --corpus plans/sdd/V6-closeout/eval/runs/pilot1-plugin-dir` — INCONCLUSIVE, not confirmatory; output identical to the 931536b record; nothing written

### Criterion changes

- verdictOf (internal/commands/cmd_eval.go) is stricter: a confirmatory live run whose pre-registered decision is inconclusive or not-applicable (LIVE-T01 undecided) now gives verdict inconclusive even with a passing replay read beside it. Before, the replay's gates could turn it into pass: at HEAD 931536b for any such run, and at base 54a4334 for such a run with zero failed trials. Rationale: the confirmatory live run is the pre-registered evaluation, the coordinator default says the verdict is its intention-to-treat decision, and a model-free replay cannot supply a verdict the live rule did not reach. A constraint regression still fails the run. This is an owner-visible policy choice the owner may overrule; it is stated in the a570ea8 commit body, docs/user-guide.md and amendment A7's closing note.

### Open issues

- I did not re-run the runpatterns and docmarkers lint subchecks after my commits. No -run pattern in any .md file changed; runpatterns already fails at base on other workstreams' reports (w2-lifetime, w2-lint, w2-sessionend), per runs/tests/generators-and-lint-windows.txt.
- Process-rule breach, now stopped: `devtool lint --only=...stubskips...` ran a whole-tree go test on the shared machine for about 25 minutes. I stopped only my own process tree, found by PID lineage; no process of mine is left.
- The confirmatory bundle must be rebuilt from the final integrated commit on a clean tree. The dry-run bundles (b8916388 and b225fcd) are not candidates, and writing any file into the tree during the bundle build makes it dirty, which --confirmatory refuses.
- The three held-out tasks and all confirmatory trials remain unrun, as required. No real Claude session was used in this seat.

### Needs the owner

- Confirm or overrule the stricter verdict rule: when a confirmatory live run's rule reaches no verdict (inconclusive or not-applicable), `qompack eval` says inconclusive even beside a passing replay. The minimal alternative the reviewer offered covers not-applicable only.
- Confirm or overrule the coordinator default carried from the implementer: a live run's failed trials are counted under intention to treat, and the verdict follows that decision instead of being forced to inconclusive.
- Before the confirmatory run, the operator must be able to truthfully attest `--known-open-defects none` for the candidate bundle (preregistration section 9); no code checks this.

